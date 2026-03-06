        const loginForm     = document.getElementById('loginForm')
        const chatContainer = document.getElementById('chatContainer')
        const messageList   = document.getElementById('messageList')
        const messageInput  = document.getElementById('messageInput')
        const sendButton    = document.getElementById('sendButton')
        const headerRoom    = document.getElementById('headerRoom')
        const headerUser    = document.getElementById('headerUser')

        let conn = null
        let user = null
        let room = null

        loginForm.addEventListener('submit', function(event) {
            event.preventDefault()

            user = document.getElementById('username').value.trim()
            room = document.getElementById('room').value.trim()

            conn = new WebSocket(`ws://${window.location.host}/ws?user=${user}&room=${room}`)

            conn.onopen = function() {
                console.log('Connection established')
                headerRoom.textContent = room
                headerUser.textContent = user
                loginForm.classList.add('hidden')
                chatContainer.classList.remove('hidden')
                messageInput.focus()
            }

            conn.onclose = function() {
                console.log('Connection closed')
            }

            conn.onmessage = function(event) {
                const message = JSON.parse(event.data)
                appendMessage(message)
            }
        })

        function appendMessage(message) {
            const el = document.createElement('div')
            const ts = message.timestamp
                ? new Date(message.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
                : ''

            if (message.type === 'chat') {
                const isOwn = message.sender === user
                el.classList.add('message', isOwn ? 'own' : 'theirs')
                el.innerHTML = `
                    <div class="meta">
                        <span class="sender">${message.sender}</span>
                        <span class="ts">${ts}</span>
                    </div>
                    <div class="body">${escapeHtml(message.content)}</div>
                `
            } else if (message.type === 'system') {
                el.classList.add('message', 'system')
                el.textContent = message.content
            }

            messageList.appendChild(el)
            el.scrollIntoView({ behavior: 'smooth', block: 'end' })
        }

        function sendChat() {
            const content = messageInput.value.trim()
            if (!content || !conn) return
            conn.send(JSON.stringify({
                type:    'chat',
                sender:  user,
                room:    room,
                content: content,
            }))
            messageInput.value = ''
        }

        function escapeHtml(str) {
            return str
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
        }

        sendButton.addEventListener('click', sendChat)
        messageInput.addEventListener('keydown', function(event) {
            if (event.key === 'Enter') sendChat()
        })