        const loginForm        = document.getElementById('loginForm')
        const chatContainer    = document.getElementById('chatContainer')
        const messageList      = document.getElementById('messageList')
        const messageInput     = document.getElementById('messageInput')
        const sendButton       = document.getElementById('sendButton')
        const headerRoom       = document.getElementById('headerRoom')
        const headerUser       = document.getElementById('headerUser')
        const passwordInput    = document.getElementById('password')
        const loginError       = document.getElementById('loginError')
        const toggleRegister   = document.getElementById('toggleRegister')
        const addRoomInput     = document.getElementById('addRoomInput')
        const addRoomButton    = document.getElementById('addRoomButton')
        const fileInput        = document.getElementById('fileInput')
        const attachButton     = document.getElementById('attachButton')
        const pendingMediaEl   = document.getElementById('pendingMedia')
        const pendingMediaName = document.getElementById('pendingMediaName')
        const clearMediaButton = document.getElementById('clearMediaButton')

        let conn              = null
        let user              = null
        let sessionId         = null
        let activeRoom        = null          // currently displayed room name
        const messages        = {}           // { [roomName]: Message[] }
        const joinedRooms     = new Set()    // insertion-ordered set of joined rooms
        const unread          = {}           // { [roomName]: number } unread counts
        let isRegisterMode    = false
        let pendingMedia      = null         // { url, type, name } or null
        let squadInfo         = null         // { name, description, your_role }
        let identityKeypair   = null         // { privateKey, publicKey } — ECDH P-256
        let ownPubKeyB64      = null         // base64(SPKI) of own public key
        const pendingKeyRooms = new Set()    // rooms waiting for key distribution
        const messageBuffer   = {}           // { [roomName]: Message[] } — encrypted msgs buffered until key arrives
        const ownedRooms      = new Set()    // rooms created/owned by the current user

        // ── Login / Register ──────────────────────────────

        toggleRegister.addEventListener('click', function(e) {
            e.preventDefault()
            isRegisterMode = !isRegisterMode
            loginForm.querySelector('button[type="submit"]').textContent =
                isRegisterMode ? 'Register →' : 'Connect →'
            toggleRegister.textContent = isRegisterMode ? 'Back to login' : 'Register'
        })

        loginForm.addEventListener('submit', async function(event) {
            event.preventDefault()
            loginError.classList.add('hidden')

            const username = document.getElementById('username').value.trim()
            const password = passwordInput.value

            if (isRegisterMode) {
                const res = await fetch('/register', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ username, password }),
                })
                if (!res.ok) {
                    loginError.textContent = 'Registration failed: ' + (await res.text()).trim()
                    loginError.classList.remove('hidden')
                    return
                }
            }

            const res = await fetch('/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, password }),
            })
            if (!res.ok) {
                loginError.textContent = 'Login failed: ' + (await res.text()).trim()
                loginError.classList.remove('hidden')
                return
            }
            const data = await res.json()
            sessionId = data.session_id
            user = username

            const wsScheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
            conn = new WebSocket(`${wsScheme}//${window.location.host}/ws?session=${sessionId}`)

            conn.onopen = async function() {
                console.log('Connection established')
                loginForm.classList.add('hidden')
                chatContainer.classList.remove('hidden')

                if (Notification.permission === 'default') {
                    Notification.requestPermission()
                }

                // Initialize E2EE identity keypair (generates on first login, loads on subsequent).
                identityKeypair = await E2EE.generateOrLoadIdentityKey(user)
                ownPubKeyB64    = await E2EE.exportPublicKey(identityKeypair.publicKey)
                // Register/update our public key with the server so other members can
                // encrypt the room key for us (e.g. when we join a new device).
                await fetch(`/keys?session=${sessionId}`, {
                    method:  'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body:    JSON.stringify({ public_key: ownPubKeyB64 }),
                })

                // Fetch squad info and render the sidebar header.
                const squadRes = await fetch(`/squad?session=${sessionId}`)
                squadInfo = await squadRes.json()
                renderSquadHeader()

                // Restore saved rooms from the server, then join each one.
                const roomsRes = await fetch(`/rooms?session=${sessionId}`)
                const roomsData = await roomsRes.json()
                for (const r of (roomsData.owned_rooms || [])) {
                    ownedRooms.add(r)
                }
                for (const roomName of roomsData.rooms) {
                    await joinRoom(roomName)
                }

                // If no saved rooms, focus the add-room input to prompt the user.
                if (joinedRooms.size === 0) {
                    addRoomInput.focus()
                } else {
                    messageInput.focus()
                }
            }

            conn.onclose = async function() {
                console.log('Connection closed')
                // Invalidate the session server-side on clean close so the token
                // cannot be reused after the user navigates away or closes the tab.
                if (sessionId) {
                    navigator.sendBeacon(`/logout?session=${sessionId}`)
                    sessionId = null
                }
            }

            conn.onmessage = async function(event) {
                const message = JSON.parse(event.data)
                // Route message to its room bucket; fall back to activeRoom for
                // any server messages that don't carry a room field.
                const msgRoom = message.room || activeRoom
                if (!msgRoom) return

                // ── E2EE key exchange handling ───────────────────────────────

                if (message.type === 'keyRequest' && message.sender !== user) {
                    // Another member needs the room key — distribute it if we have it.
                    const roomKey = await E2EE.getRoomKey(msgRoom)
                    if (!roomKey) return // we don't have it either
                    try {
                        const theirPub = await E2EE.importPublicKey(message.publicKey)
                        const { encrypted_key, key_iv } = await E2EE.encryptRoomKeyForUser(
                            roomKey, identityKeypair.privateKey, theirPub
                        )
                        // Deliver over WebSocket for immediate decryption.
                        conn.send(JSON.stringify({
                            type:    'keyDistribute',
                            room:    msgRoom,
                            content: JSON.stringify({
                                for:              message.sender,
                                encrypted_key,
                                key_iv,
                                sender_public_key: ownPubKeyB64,
                            }),
                        }))
                        // Persist to server so the recipient can load it on future logins.
                        fetch(`/room-key?session=${sessionId}`, {
                            method:  'POST',
                            headers: { 'Content-Type': 'application/json' },
                            body:    JSON.stringify({
                                room:             msgRoom,
                                for_user:         message.sender,
                                encrypted_key,
                                key_iv,
                                sender_public_key: ownPubKeyB64,
                            }),
                        })
                    } catch (err) {
                        console.error('keyRequest handling failed:', err)
                    }
                    return
                }

                if (message.type === 'keyDistribute') {
                    // We received an encrypted room key — attempt to decrypt it.
                    try {
                        const payload = JSON.parse(message.content)
                        if (payload.for !== user) return // not for us
                        const key = await E2EE.decryptRoomKey(
                            payload.encrypted_key,
                            payload.key_iv,
                            payload.sender_public_key,
                            identityKeypair.privateKey,
                            msgRoom,
                        )
                        pendingKeyRooms.delete(msgRoom)
                        // Decrypt and replay any buffered messages for this room.
                        if (messageBuffer[msgRoom]) {
                            for (const buffered of messageBuffer[msgRoom]) {
                                try {
                                    buffered.content = await E2EE.decryptMessage(buffered.content, buffered.iv, key)
                                } catch (_) { buffered.content = '[decryption failed]' }
                                if (!messages[msgRoom]) messages[msgRoom] = []
                                messages[msgRoom].push(buffered)
                            }
                            delete messageBuffer[msgRoom]
                        }
                        // Decrypt any history messages that are still showing as encrypted.
                        if (messages[msgRoom]) {
                            for (const m of messages[msgRoom]) {
                                if (m.encrypted && m._raw) {
                                    try {
                                        m.content = await E2EE.decryptMessage(m._raw, m.iv, key)
                                    } catch (_) { m.content = '[decryption failed]' }
                                    m.encrypted = false
                                }
                            }
                        }
                        if (msgRoom === activeRoom) renderMessageList()
                    } catch (err) {
                        console.error('keyDistribute handling failed:', err)
                    }
                    return
                }

                // ── Decrypt chat messages ────────────────────────────────────

                if (message.type === 'chat' && message.encrypted) {
                    const roomKey = await E2EE.getRoomKey(msgRoom)
                    if (!roomKey) {
                        // Key not yet available — buffer the message.
                        if (!messageBuffer[msgRoom]) messageBuffer[msgRoom] = []
                        messageBuffer[msgRoom].push(message)
                        return
                    }
                    try {
                        message.content = await E2EE.decryptMessage(message.content, message.iv, roomKey)
                    } catch (_) {
                        message.content = '[decryption failed]'
                    }
                }

                // ── Room deletion by owner ───────────────────────────────────

                if (message.type === 'system' && message.content === 'this room has been deleted' && msgRoom) {
                    await E2EE.deleteRoomKey(msgRoom)
                    ownedRooms.delete(msgRoom)
                    joinedRooms.delete(msgRoom)
                    delete messages[msgRoom]
                    renderSidebar()
                    if (activeRoom === msgRoom) {
                        const remaining = [...joinedRooms]
                        activeRoom = remaining.length > 0 ? remaining[0] : null
                        if (activeRoom) {
                            switchToRoom(activeRoom)
                        } else {
                            headerRoom.textContent = ''
                            messageList.innerHTML = ''
                        }
                    }
                    return
                }

                // ── Key rotation on member leave ─────────────────────────────

                if (message.type === 'system' && message.content && message.content.endsWith(' left the room')) {
                    // Elect coordinator: alphabetically first current member rotates the key.
                    rotateRoomKeyIfCoordinator(msgRoom)
                }

                // ── Normal message routing ───────────────────────────────────

                if (!messages[msgRoom]) messages[msgRoom] = []
                messages[msgRoom].push(message)
                // Only render to DOM if the message belongs to the visible room.
                if (msgRoom === activeRoom) {
                    appendMessageToDOM(message, messageList)
                } else if (message.type === 'chat') {
                    unread[msgRoom] = (unread[msgRoom] || 0) + 1
                    renderSidebar()
                    updateTitle()
                    if (msgRoom.startsWith('dm:') && Notification.permission === 'granted' && !document.hasFocus()) {
                        new Notification(`DM from ${message.sender}`, {
                            body: message.content || '📎 media',
                            tag:  msgRoom,
                        })
                    }
                }
            }
        })

        // ── Room management ───────────────────────────────

        // joinRoom persists the room to the server, sends a WS joinRoom message,
        // and updates local state + sidebar. Idempotent: switching to an already-
        // joined room just calls switchToRoom.
        async function joinRoom(roomName) {
            if (joinedRooms.has(roomName)) {
                switchToRoom(roomName)
                return
            }
            const postRes = await fetch(`/rooms?session=${sessionId}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ room: roomName }),
            })
            if (postRes.ok) {
                try {
                    const postData = await postRes.json()
                    if (postData.owner) ownedRooms.add(roomName)
                } catch (_) {}
            }
            conn.send(JSON.stringify({ type: 'joinRoom', room: roomName }))
            joinedRooms.add(roomName)

            // Load history before resolving the room key so we can decide
            // whether this is the first join (empty history → generate key).
            const histRes = await fetch(`/history?room=${encodeURIComponent(roomName)}&session=${sessionId}`)
            const histMsgs = histRes.ok ? (await histRes.json()).messages : []

            // ── Room key setup ───────────────────────────────────────────────
            await setupRoomKey(roomName, histMsgs)

            // Decrypt any history messages that are encrypted.
            const roomKey = await E2EE.getRoomKey(roomName)
            messages[roomName] = []
            for (const m of histMsgs) {
                if (m.encrypted && roomKey) {
                    try {
                        m.content = await E2EE.decryptMessage(m.content, m.iv, roomKey)
                    } catch (_) {
                        m._raw     = m.content
                        m.content  = '[encrypted — waiting for key]'
                    }
                } else if (m.encrypted && !roomKey) {
                    m._raw    = m.content
                    m.content = '[encrypted — waiting for key]'
                }
                messages[roomName].push(m)
            }

            renderSidebar()
            if (activeRoom === null) switchToRoom(roomName)
        }

        // setupRoomKey fetches or generates the room key for roomName.
        // histMsgs is the list of historical messages (used to detect first join).
        async function setupRoomKey(roomName, histMsgs) {
            // Try to load existing key from IndexedDB first (avoids a server round-trip on re-render).
            const cached = await E2EE.getRoomKey(roomName)
            if (cached) return

            // Try to fetch our encrypted copy from the server.
            const res = await fetch(`/room-key?room=${encodeURIComponent(roomName)}&session=${sessionId}`)
            if (res.ok) {
                const data = await res.json()
                try {
                    await E2EE.decryptRoomKey(
                        data.encrypted_key,
                        data.key_iv,
                        data.sender_public_key,
                        identityKeypair.privateKey,
                        roomName,
                    )
                } catch (err) {
                    console.error('Failed to decrypt room key from server:', err)
                }
                return
            }

            // No key on server for us yet.
            const hasEncryptedHistory = histMsgs.some(m => m.encrypted)
            if (histMsgs.length === 0 || !hasEncryptedHistory) {
                // New room or pre-E2EE room with no encrypted messages — generate a fresh key.
                const newKey = await E2EE.generateRoomKey()
                await E2EE.storeRoomKey(roomName, newKey)
                const { encrypted_key, key_iv } = await E2EE.encryptRoomKeyForUser(
                    newKey, identityKeypair.privateKey, identityKeypair.publicKey
                )
                await fetch(`/room-key?session=${sessionId}`, {
                    method:  'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body:    JSON.stringify({
                        room:             roomName,
                        for_user:         user,
                        encrypted_key,
                        key_iv,
                        sender_public_key: ownPubKeyB64,
                    }),
                })
            } else {
                // Existing room — request the key from an online member.
                pendingKeyRooms.add(roomName)
                conn.send(JSON.stringify({
                    type:      'keyRequest',
                    room:      roomName,
                    publicKey: ownPubKeyB64,
                }))
            }
        }

        // leaveRoom closes a DM conversation for the current user only.
        // Non-DM rooms cannot be left; use deleteRoom instead.
        async function leaveRoom(roomName) {
            await fetch(`/rooms?session=${sessionId}`, {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ room: roomName }),
            })
            conn.send(JSON.stringify({ type: 'leaveRoom', room: roomName }))
            joinedRooms.delete(roomName)
            delete messages[roomName]
            renderSidebar()
            if (activeRoom === roomName) {
                const remaining = [...joinedRooms]
                activeRoom = remaining.length > 0 ? remaining[0] : null
                if (activeRoom) {
                    switchToRoom(activeRoom)
                } else {
                    headerRoom.textContent = ''
                    messageList.innerHTML = ''
                }
            }
        }

        // deleteRoom permanently deletes a room and all its data. Owner only.
        async function deleteRoom(roomName) {
            if (!confirm(`Delete #${roomName}? This permanently removes all messages and cannot be undone.`)) return
            const res = await fetch(`/room?room=${encodeURIComponent(roomName)}&session=${sessionId}`, {
                method: 'DELETE',
            })
            if (!res.ok) {
                alert('Delete failed: ' + (await res.text()).trim())
                return
            }
            await E2EE.deleteRoomKey(roomName)
            ownedRooms.delete(roomName)
            joinedRooms.delete(roomName)
            delete messages[roomName]
            renderSidebar()
            if (activeRoom === roomName) {
                const remaining = [...joinedRooms]
                activeRoom = remaining.length > 0 ? remaining[0] : null
                if (activeRoom) {
                    switchToRoom(activeRoom)
                } else {
                    headerRoom.textContent = ''
                    messageList.innerHTML = ''
                }
            }
        }

        // switchToRoom updates the active room, header, sidebar highlight, and
        // re-renders the message list from the stored history for that room.
        function switchToRoom(roomName) {
            activeRoom = roomName
            delete unread[roomName]
            updateTitle()
            const hashEl = document.querySelector('#chatHeader .hash')
            if (roomName.startsWith('dm:')) {
                const parts = roomName.split(':')
                const lowerUser = user.toLowerCase()
                const otherUser = parts[1] === lowerUser ? parts[2] : parts[1]
                hashEl.textContent = '@'
                headerRoom.textContent = otherUser
            } else {
                hashEl.textContent = '#'
                headerRoom.textContent = roomName
            }
            renderSidebar()
            renderMessageList()
        }

        function updateTitle() {
            const total = Object.values(unread).reduce((sum, n) => sum + n, 0)
            document.title = total > 0 ? `(${total}) sethchat` : 'sethchat'
        }

        // ── Rendering ─────────────────────────────────────

        // renderSidebar rebuilds the #roomList element from the joinedRooms Set.
        function renderSidebar() {
            const list = document.getElementById('roomList')
            list.innerHTML = ''
            const canManage = squadInfo && (squadInfo.your_role === 'owner' || squadInfo.your_role === 'admin')
            for (const name of joinedRooms) {
                if (name.startsWith('dm:')) continue
                const count = unread[name] || 0
                const isOwner = ownedRooms.has(name)
                const item = document.createElement('div')
                item.className = 'room-item' + (name === activeRoom ? ' active' : '')
                item.innerHTML = `
                    <span class="room-hash">#</span>
                    <span class="room-label">${escapeHtml(name)}</span>
                    <span class="room-end">
                        ${count ? `<span class="unread-badge">${count}</span>` : ''}
                        ${canManage ? `<button class="settings-btn" title="room settings">⚙</button>` : ''}
                        ${isOwner ? `<button class="delete-btn" title="delete room">✕</button>` : ''}
                    </span>
                `
                item.querySelector('.room-label').addEventListener('click', () => switchToRoom(name))
                item.querySelector('.room-hash').addEventListener('click', () => switchToRoom(name))
                if (canManage) {
                    item.querySelector('.settings-btn').addEventListener('click', e => {
                        e.stopPropagation()
                        openRoomSettings(name)
                    })
                }
                if (isOwner) {
                    item.querySelector('.delete-btn').addEventListener('click', e => {
                        e.stopPropagation()
                        deleteRoom(name)
                    })
                }
                list.appendChild(item)
            }
            renderDMList()
        }

        function openRoomSettings(roomName) {
            openModal(`#${escapeHtml(roomName)} settings`, `
                <p class="modal-hint">Permanently deletes all messages. This cannot be undone.</p>
                <button id="clearHistoryBtn" class="danger-btn">Clear message history</button>
                <p id="clearHistoryMsg" class="modal-status"></p>
            `)
            document.getElementById('clearHistoryBtn').addEventListener('click', async () => {
                if (!confirm(`Clear all message history for #${roomName}? This cannot be undone.`)) return
                const res = await fetch(`/history?room=${encodeURIComponent(roomName)}&session=${sessionId}`, {
                    method: 'DELETE',
                })
                if (res.ok) {
                    messages[roomName] = []
                    if (activeRoom === roomName) renderMessageList()
                    document.getElementById('clearHistoryMsg').textContent = 'History cleared.'
                    document.getElementById('clearHistoryBtn').disabled = true
                } else {
                    document.getElementById('clearHistoryMsg').textContent = 'Failed: ' + (await res.text()).trim()
                }
            })
        }

        // renderDMList rebuilds the #dmList element from DM entries in joinedRooms.
        function renderDMList() {
            const list = document.getElementById('dmList')
            list.innerHTML = ''
            for (const name of joinedRooms) {
                if (!name.startsWith('dm:')) continue
                const parts = name.split(':')
                const lowerUser = user.toLowerCase()
                const otherUser = parts[1] === lowerUser ? parts[2] : parts[1]
                const count = unread[name] || 0
                const item = document.createElement('div')
                item.className = 'room-item' + (name === activeRoom ? ' active' : '')
                item.innerHTML = `
                    <span class="room-hash dm-sigil">@</span>
                    <span class="room-label">${escapeHtml(otherUser)}</span>
                    <span class="room-end">
                        ${count ? `<span class="unread-badge">${count}</span>` : ''}
                        <button class="leave-btn" title="close">✕</button>
                    </span>
                `
                item.querySelector('.room-label').addEventListener('click', () => switchToRoom(name))
                item.querySelector('.dm-sigil').addEventListener('click', () => switchToRoom(name))
                item.querySelector('.leave-btn').addEventListener('click', e => {
                    e.stopPropagation()
                    leaveRoom(name)
                })
                list.appendChild(item)
            }
        }

        // renderMessageList replaces the full #messageList DOM from stored history.
        // Used when switching rooms.
        function renderMessageList() {
            messageList.innerHTML = ''
            if (!activeRoom || !messages[activeRoom]) return
            for (const msg of messages[activeRoom]) {
                appendMessageToDOM(msg, messageList)
            }
            messageList.lastElementChild?.scrollIntoView({ behavior: 'instant', block: 'end' })
        }

        // appendMessageToDOM creates and appends a message element to container.
        function appendMessageToDOM(message, container) {
            const el = document.createElement('div')
            const ts = message.timestamp
                ? new Date(message.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
                : ''

            if (message.type === 'chat') {
                const isOwn = message.sender === user
                el.classList.add('message', isOwn ? 'own' : 'theirs')

                let mediaHTML = ''
                if (message.mediaURL) {
                    if (message.mediaType && message.mediaType.startsWith('video/')) {
                        mediaHTML = `<div class="media-content">
                            <video src="${escapeHtml(message.mediaURL)}" controls></video>
                        </div>`
                    } else {
                        // images and GIFs
                        mediaHTML = `<div class="media-content">
                            <img src="${escapeHtml(message.mediaURL)}"
                                 alt="attached media"
                                 class="clickable-media">
                        </div>`
                    }
                }

                const bodyHTML = message.content
                    ? `<div class="body">${escapeHtml(message.content)}</div>`
                    : ''

                el.innerHTML = `
                    <div class="meta">
                        <span class="sender">${escapeHtml(message.sender)}</span>
                        <span class="ts">${ts}</span>
                    </div>
                    ${bodyHTML}
                    ${mediaHTML}
                `
                const img = el.querySelector('.clickable-media')
                if (img) img.addEventListener('click', () => window.open(img.src))
            } else if (message.type === 'system') {
                el.classList.add('message', 'system')
                el.textContent = message.content
            }

            container.appendChild(el)
            el.scrollIntoView({ behavior: 'smooth', block: 'end' })
        }

        // ── Media attachment ──────────────────────────────

        attachButton.addEventListener('click', () => fileInput.click())

        fileInput.addEventListener('change', async function() {
            const file = fileInput.files[0]
            if (!file) return
            fileInput.value = '' // reset so same file can be re-selected
            await uploadFile(file)
        })

        // Paste an image directly from the clipboard (e.g. a screenshot).
        messageInput.addEventListener('paste', async function(e) {
            const items = e.clipboardData?.items
            if (!items) return
            for (const item of items) {
                if (item.type.startsWith('image/')) {
                    e.preventDefault() // don't paste raw data-url text
                    const file = item.getAsFile()
                    if (file) await uploadFile(file)
                    break
                }
            }
        })

        async function uploadFile(file) {
            const formData = new FormData()
            formData.append('file', file)

            const res = await fetch(`/upload?session=${sessionId}`, {
                method: 'POST',
                body: formData,
            })
            if (!res.ok) {
                alert('Upload failed: ' + (await res.text()).trim())
                return
            }
            const data = await res.json()
            // Use the original filename if available, otherwise label as pasted image.
            const displayName = file.name && file.name !== 'image.png'
                ? file.name
                : 'pasted image'
            pendingMedia = { url: data.url, type: data.type, name: displayName }
            pendingMediaName.textContent = '📎 ' + displayName
            pendingMediaEl.classList.remove('hidden')
            attachButton.classList.add('has-media')
            messageInput.focus()
        }

        clearMediaButton.addEventListener('click', clearPendingMedia)

        function clearPendingMedia() {
            pendingMedia = null
            pendingMediaEl.classList.add('hidden')
            pendingMediaName.textContent = ''
            attachButton.classList.remove('has-media')
        }

        // ── Sending messages ──────────────────────────────

        async function sendChat() {
            const content = messageInput.value.trim()
            if (!content && !pendingMedia) return
            if (!conn || !activeRoom) return

            const roomKey = await E2EE.getRoomKey(activeRoom)
            if (!roomKey) {
                // Room key not yet available — show transient hint and bail.
                const orig = messageInput.placeholder
                messageInput.placeholder = 'Waiting for room key from an online member…'
                setTimeout(() => { messageInput.placeholder = orig }, 3000)
                return
            }

            const msg = {
                type:   'chat',
                sender: user,
                room:   activeRoom,
            }

            // Encrypt text content if present.
            if (content) {
                const { ciphertext, iv } = await E2EE.encryptMessage(content, roomKey)
                msg.content   = ciphertext
                msg.iv        = iv
                msg.encrypted = true
            } else {
                msg.content = ''
            }

            if (pendingMedia) {
                msg.mediaURL  = pendingMedia.url
                msg.mediaType = pendingMedia.type
                clearPendingMedia()
            }

            conn.send(JSON.stringify(msg))
            messageInput.value = ''
        }

        sendButton.addEventListener('click', sendChat)
        messageInput.addEventListener('keydown', function(event) {
            if (event.key === 'Enter') sendChat()
        })

        // ── Add-room UI ───────────────────────────────────

        addRoomButton.addEventListener('click', addRoomFromInput)
        addRoomInput.addEventListener('keydown', function(e) {
            if (e.key === 'Enter') addRoomFromInput()
        })

        function addRoomFromInput() {
            const name = addRoomInput.value.trim()
            if (!name || !conn) return
            addRoomInput.value = ''
            joinRoom(name)
        }

        // ── Direct messages ───────────────────────────────

        document.getElementById('newDMButton').addEventListener('click', openDMPicker)

        async function openDMPicker() {
            const res = await fetch(`/users?session=${sessionId}`)
            const data = await res.json()
            if (!data.users || data.users.length === 0) {
                openModal('New Direct Message', '<p class="modal-hint">No other members yet.</p>')
                return
            }
            const rows = data.users.map(u =>
                `<div class="member-item dm-picker-item" data-name="${escapeHtml(u.name)}">
                    <span class="member-name">@ ${escapeHtml(u.name)}</span>
                </div>`
            ).join('')
            openModal('New Direct Message', rows)
            modalBody.querySelectorAll('.dm-picker-item').forEach(item => {
                item.addEventListener('click', async () => {
                    closeModal()
                    const targetName = item.dataset.name
                    const dmRes = await fetch(`/dm?session=${sessionId}`, {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ username: targetName }),
                    })
                    const dmData = await dmRes.json()
                    joinRoom(dmData.room)
                })
            })
        }

        // ── Squad header ──────────────────────────────────

        function renderSquadHeader() {
            document.getElementById('squadName').textContent = squadInfo.name

            const roleLabels = { owner: '♛ owner', admin: '★ admin', member: '' }
            document.getElementById('headerUser').textContent = user
            const badge = document.getElementById('userRoleBadge')
            badge.textContent = roleLabels[squadInfo.your_role] || ''
            badge.className = 'role-' + squadInfo.your_role

            // Settings button only visible to owner
            const settingsBtn = document.getElementById('settingsButton')
            if (squadInfo.your_role === 'owner') {
                settingsBtn.classList.remove('hidden')
            } else {
                settingsBtn.classList.add('hidden')
            }
        }

        // ── Modal helpers ─────────────────────────────────

        const modalOverlay = document.getElementById('modalOverlay')
        const modalTitle   = document.getElementById('modalTitle')
        const modalBody    = document.getElementById('modalBody')
        const modalClose   = document.getElementById('modalClose')

        function openModal(title, bodyHTML) {
            modalTitle.textContent = title
            modalBody.innerHTML = bodyHTML
            modalOverlay.classList.remove('hidden')
        }

        function closeModal() {
            modalOverlay.classList.add('hidden')
            modalBody.innerHTML = ''
        }

        modalClose.addEventListener('click', closeModal)
        modalOverlay.addEventListener('click', e => { if (e.target === modalOverlay) closeModal() })

        // ── Settings panel (owner only) ───────────────────

        document.getElementById('settingsButton').addEventListener('click', openSettingsPanel)

        function openSettingsPanel() {
            openModal('Squad Settings', `
                <div class="field">
                    <label>Squad Name</label>
                    <input type="text" id="settingsName" value="${escapeHtml(squadInfo.name)}">
                </div>
                <div class="field">
                    <label>Description</label>
                    <input type="text" id="settingsDesc" value="${escapeHtml(squadInfo.description)}">
                </div>
                <button id="saveSettingsBtn" class="modal-inline-btn">Save</button>
                <p id="settingsMsg" class="modal-status"></p>
            `)
            document.getElementById('saveSettingsBtn').addEventListener('click', async () => {
                const name = document.getElementById('settingsName').value.trim()
                const description = document.getElementById('settingsDesc').value.trim()
                if (!name) return
                const res = await fetch(`/squad?session=${sessionId}`, {
                    method: 'PATCH',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ name, description }),
                })
                if (res.ok) {
                    squadInfo.name = name
                    squadInfo.description = description
                    renderSquadHeader()
                    document.getElementById('settingsMsg').textContent = 'Saved.'
                }
            })
        }

        // ── Members panel ─────────────────────────────────

        document.getElementById('membersButton').addEventListener('click', openMembersPanel)

        async function openMembersPanel() {
            const res = await fetch(`/squad/members?session=${sessionId}`)
            const data = await res.json()
            const isOwner = squadInfo.your_role === 'owner'
            const roleLabels = { owner: '♛ owner', admin: '★ admin', member: 'member' }

            const rows = data.members.map(m => {
                const roleDisplay = isOwner && m.role !== 'owner'
                    ? `<select class="role-select" data-uid="${m.id}">
                        <option value="admin"  ${m.role === 'admin'  ? 'selected' : ''}>admin</option>
                        <option value="member" ${m.role === 'member' ? 'selected' : ''}>member</option>
                       </select>`
                    : `<span class="member-role role-${m.role}">${roleLabels[m.role]}</span>`

                return `<div class="member-item">
                    <span class="member-name">${escapeHtml(m.name)}</span>
                    ${roleDisplay}
                </div>`
            }).join('')

            openModal('Members', rows)

            // Wire up role selects (owner only)
            if (isOwner) {
                modalBody.querySelectorAll('.role-select').forEach(sel => {
                    sel.addEventListener('change', async function() {
                        await fetch(`/squad/members?session=${sessionId}`, {
                            method: 'PATCH',
                            headers: { 'Content-Type': 'application/json' },
                            body: JSON.stringify({ user_id: parseInt(this.dataset.uid), role: this.value }),
                        })
                    })
                })
            }
        }

        // ── Key rotation ──────────────────────────────────

        // rotateRoomKeyIfCoordinator generates a new room key when a member leaves,
        // but only if this client is the alphabetically-first current member (to
        // avoid every member doing the same work simultaneously).
        async function rotateRoomKeyIfCoordinator(roomName) {
            if (!joinedRooms.has(roomName)) return
            const roomKey = await E2EE.getRoomKey(roomName)
            if (!roomKey) return

            // Fetch current member list to determine coordinator.
            const res = await fetch(`/room/members?room=${encodeURIComponent(roomName)}&session=${sessionId}`)
            if (!res.ok) return
            const { members } = await res.json()
            if (!members || members.length === 0) return

            // Only the alphabetically-first member performs rotation.
            const sorted = [...members].sort()
            if (sorted[0].toLowerCase() !== user.toLowerCase()) return

            // Generate new room key.
            const newKey = await E2EE.generateRoomKey()
            await E2EE.storeRoomKey(roomName, newKey)

            // Encrypt and persist for all current members (including self).
            for (const memberName of members) {
                try {
                    const keyRes = await fetch(`/keys?username=${encodeURIComponent(memberName)}&session=${sessionId}`)
                    if (!keyRes.ok) continue
                    const { public_key } = await keyRes.json()
                    const theirPub = await E2EE.importPublicKey(public_key)
                    const { encrypted_key, key_iv } = await E2EE.encryptRoomKeyForUser(
                        newKey, identityKeypair.privateKey, theirPub
                    )
                    await fetch(`/room-key?session=${sessionId}`, {
                        method:  'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body:    JSON.stringify({
                            room:             roomName,
                            for_user:         memberName,
                            encrypted_key,
                            key_iv,
                            sender_public_key: ownPubKeyB64,
                        }),
                    })
                } catch (err) {
                    console.error(`Key rotation failed for ${memberName}:`, err)
                }
            }
        }

        // ── Utilities ─────────────────────────────────────

        // Invalidate session when the page unloads (tab close, navigation away).
        // sendBeacon is used because it survives the page teardown.
        window.addEventListener('beforeunload', () => {
            if (sessionId) {
                navigator.sendBeacon(`/logout?session=${sessionId}`)
                sessionId = null
            }
        })

        function escapeHtml(str) {
            return str
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
        }
