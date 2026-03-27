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

        let conn           = null
        let user           = null
        let sessionId      = null
        let activeRoom     = null          // currently displayed room name
        const messages     = {}           // { [roomName]: Message[] }
        const joinedRooms  = new Set()    // insertion-ordered set of joined rooms
        let isRegisterMode = false
        let pendingMedia   = null         // { url, type, name } or null
        let squadInfo      = null         // { name, description, your_role }

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

                // Fetch squad info and render the sidebar header.
                const squadRes = await fetch(`/squad?session=${sessionId}`)
                squadInfo = await squadRes.json()
                renderSquadHeader()

                // Restore saved rooms from the server, then join each one.
                const roomsRes = await fetch(`/rooms?session=${sessionId}`)
                const roomsData = await roomsRes.json()
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

            conn.onclose = function() {
                console.log('Connection closed')
            }

            conn.onmessage = function(event) {
                const message = JSON.parse(event.data)
                // Route message to its room bucket; fall back to activeRoom for
                // any server messages that don't carry a room field.
                const msgRoom = message.room || activeRoom
                if (!msgRoom) return
                if (!messages[msgRoom]) messages[msgRoom] = []
                messages[msgRoom].push(message)
                // Only render to DOM if the message belongs to the visible room.
                if (msgRoom === activeRoom) {
                    appendMessageToDOM(message, messageList)
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
            await fetch(`/rooms?session=${sessionId}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ room: roomName }),
            })
            conn.send(JSON.stringify({ type: 'joinRoom', room: roomName }))
            joinedRooms.add(roomName)
            messages[roomName] = []
            renderSidebar()
            if (activeRoom === null) switchToRoom(roomName)
        }

        // leaveRoom removes the room from the server, sends a WS leaveRoom message,
        // and updates local state + sidebar. If the active room is left, auto-
        // switches to the next available room (or clears the message pane).
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

        // switchToRoom updates the active room, header, sidebar highlight, and
        // re-renders the message list from the stored history for that room.
        function switchToRoom(roomName) {
            activeRoom = roomName
            headerRoom.textContent = roomName
            renderSidebar()
            renderMessageList()
        }

        // ── Rendering ─────────────────────────────────────

        // renderSidebar rebuilds the #roomList element from the joinedRooms Set.
        function renderSidebar() {
            const list = document.getElementById('roomList')
            list.innerHTML = ''
            for (const name of joinedRooms) {
                const item = document.createElement('div')
                item.className = 'room-item' + (name === activeRoom ? ' active' : '')
                item.innerHTML = `
                    <span class="room-hash">#</span>
                    <span class="room-label">${escapeHtml(name)}</span>
                    <button class="leave-btn" title="leave">✕</button>
                `
                item.querySelector('.room-label').addEventListener('click', () => switchToRoom(name))
                item.querySelector('.room-hash').addEventListener('click', () => switchToRoom(name))
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
                                 onclick="window.open(this.src)">
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

        function sendChat() {
            const content = messageInput.value.trim()
            if (!content && !pendingMedia) return
            if (!conn || !activeRoom) return

            const msg = {
                type:    'chat',
                sender:  user,
                room:    activeRoom,
                content: content,
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
                <button id="saveSettingsBtn" style="align-self:flex-start">Save</button>
                <p id="settingsMsg" style="font-size:0.78rem;color:var(--accent)"></p>
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

        // ── Utilities ─────────────────────────────────────

        function escapeHtml(str) {
            return str
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
        }
