        const loginForm     = document.getElementById('loginForm')
        const chatContainer = document.getElementById('chatContainer')
        const messageList   = document.getElementById('messageList')
        const messageInput  = document.getElementById('messageInput')
        const sendButton    = document.getElementById('sendButton')
        const headerRoom    = document.getElementById('headerRoom')
        const headerUser    = document.getElementById('headerUser')
        const passwordInput  = document.getElementById('password')
        const loginError     = document.getElementById('loginError')
        const toggleRegister = document.getElementById('toggleRegister')

        let conn = null
        let user = null
        let room = null
        let sessionId = null
        let isRegisterMode = false

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
            room = document.getElementById('room').value.trim()

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

            conn = new WebSocket(`ws://${window.location.host}/ws?session=${sessionId}&room=${room}`)

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
