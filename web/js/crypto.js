// E2EE module for Sethchat.
//
// Crypto scheme:
//   - Identity keys: ECDH P-256 keypair per user (private key non-extractable, stored in IndexedDB)
//   - Room keys:     AES-256-GCM symmetric key per room (stored in IndexedDB)
//   - Key wrapping:  ECDH(myPriv, theirPub) → HKDF-SHA-256 → AES-256-GCM wrap of room key
//   - Messages:      AES-256-GCM with a fresh 12-byte IV per message
//
// All IndexedDB operations are async. Private keys are non-extractable — they
// never leave the browser's crypto subsystem.

const E2EE = (() => {
    const DB_NAME    = 'sethchat-keys'
    const DB_VERSION = 1
    const STORE      = 'keys'
    const HKDF_INFO  = new TextEncoder().encode('sethchat-room-key-v1')

    // ── IndexedDB helpers ─────────────────────────────────────────────────────

    function openDB() {
        return new Promise((resolve, reject) => {
            const req = indexedDB.open(DB_NAME, DB_VERSION)
            req.onupgradeneeded = e => e.target.result.createObjectStore(STORE)
            req.onsuccess = e => resolve(e.target.result)
            req.onerror   = e => reject(e.target.error)
        })
    }

    async function dbGet(key) {
        const db = await openDB()
        return new Promise((resolve, reject) => {
            const tx  = db.transaction(STORE, 'readonly')
            const req = tx.objectStore(STORE).get(key)
            req.onsuccess = e => resolve(e.target.result)
            req.onerror   = e => reject(e.target.error)
        })
    }

    async function dbPut(key, value) {
        const db = await openDB()
        return new Promise((resolve, reject) => {
            const tx  = db.transaction(STORE, 'readwrite')
            const req = tx.objectStore(STORE).put(value, key)
            req.onsuccess = () => resolve()
            req.onerror   = e => reject(e.target.error)
        })
    }

    // ── Base64 helpers ────────────────────────────────────────────────────────

    function toBase64(buf) {
        return btoa(String.fromCharCode(...new Uint8Array(buf)))
    }

    function fromBase64(b64) {
        return Uint8Array.from(atob(b64), c => c.charCodeAt(0))
    }

    // ── Identity key management ───────────────────────────────────────────────

    // generateOrLoadIdentityKey returns the user's ECDH P-256 keypair,
    // generating and persisting it on first call. The private key is
    // non-extractable — it cannot be read out of the browser's crypto engine.
    async function generateOrLoadIdentityKey(username) {
        const storeKey = `ecdh-private-${username}`
        const existing = await dbGet(storeKey)
        if (existing) return existing

        const keypair = await crypto.subtle.generateKey(
            { name: 'ECDH', namedCurve: 'P-256' },
            false,          // non-extractable private key
            ['deriveKey', 'deriveBits'],
        )
        await dbPut(storeKey, keypair)
        return keypair
    }

    // exportPublicKey returns the base64-encoded SPKI representation of a
    // public key, importable by both WebCrypto and Go's crypto/ecdh.
    async function exportPublicKey(publicKey) {
        const spki = await crypto.subtle.exportKey('spki', publicKey)
        return toBase64(spki)
    }

    // importPublicKey imports a base64-encoded SPKI public key for use in
    // ECDH operations. extractable=true so it can be re-exported if needed.
    async function importPublicKey(b64Spki) {
        return crypto.subtle.importKey(
            'spki',
            fromBase64(b64Spki),
            { name: 'ECDH', namedCurve: 'P-256' },
            true,
            [],
        )
    }

    // ── Key derivation ────────────────────────────────────────────────────────

    // deriveSharedAESKey performs ECDH + HKDF to produce a 256-bit AES-GCM key
    // from two parties' keys. Both sides with swapped arguments produce the same
    // key (ECDH is commutative).
    async function deriveSharedAESKey(myPrivateKey, theirPublicKey) {
        const sharedBits = await crypto.subtle.deriveBits(
            { name: 'ECDH', public: theirPublicKey },
            myPrivateKey,
            256,
        )
        const baseKey = await crypto.subtle.importKey(
            'raw', sharedBits, 'HKDF', false, ['deriveKey'],
        )
        return crypto.subtle.deriveKey(
            {
                name: 'HKDF',
                hash: 'SHA-256',
                salt: new Uint8Array(32), // fixed zero salt
                info: HKDF_INFO,
            },
            baseKey,
            { name: 'AES-GCM', length: 256 },
            true,   // extractable so we can use it to wrap/unwrap room keys
            ['encrypt', 'decrypt'],
        )
    }

    // ── Room key management ───────────────────────────────────────────────────

    // generateRoomKey creates a fresh AES-256-GCM key for a room.
    function generateRoomKey() {
        return crypto.subtle.generateKey(
            { name: 'AES-GCM', length: 256 },
            true,   // extractable so we can encrypt it for distribution
            ['encrypt', 'decrypt'],
        )
    }

    // getRoomKey returns the stored CryptoKey for a room, or null if not found.
    async function getRoomKey(roomName) {
        return (await dbGet(`room-key-${roomName}`)) || null
    }

    // storeRoomKey persists a room CryptoKey to IndexedDB.
    async function storeRoomKey(roomName, key) {
        await dbPut(`room-key-${roomName}`, key)
    }

    // encryptRoomKeyForUser wraps the room key for a recipient using the
    // ECDH-derived shared secret between our private key and their public key.
    // Returns { encrypted_key, key_iv } as base64 strings.
    async function encryptRoomKeyForUser(roomKey, myPrivateKey, theirPublicKey) {
        const sharedKey  = await deriveSharedAESKey(myPrivateKey, theirPublicKey)
        const rawRoomKey = await crypto.subtle.exportKey('raw', roomKey)
        const iv         = crypto.getRandomValues(new Uint8Array(12))
        const ciphertext = await crypto.subtle.encrypt(
            { name: 'AES-GCM', iv },
            sharedKey,
            rawRoomKey,
        )
        return {
            encrypted_key: toBase64(ciphertext),
            key_iv:        toBase64(iv),
        }
    }

    // decryptRoomKey unwraps an encrypted room key using the ECDH-derived
    // shared secret. Stores the result in IndexedDB and returns the CryptoKey.
    async function decryptRoomKey(encryptedKey, keyIv, senderPubKeyB64, myPrivateKey, roomName) {
        const senderPub  = await importPublicKey(senderPubKeyB64)
        const sharedKey  = await deriveSharedAESKey(myPrivateKey, senderPub)
        const rawRoomKey = await crypto.subtle.decrypt(
            { name: 'AES-GCM', iv: fromBase64(keyIv) },
            sharedKey,
            fromBase64(encryptedKey),
        )
        const key = await crypto.subtle.importKey(
            'raw', rawRoomKey,
            { name: 'AES-GCM', length: 256 },
            false,  // non-extractable after import — no need to re-export
            ['encrypt', 'decrypt'],
        )
        if (roomName) await storeRoomKey(roomName, key)
        return key
    }

    // ── Message encryption ────────────────────────────────────────────────────

    // encryptMessage encrypts plaintext with the room key.
    // Returns { ciphertext, iv } as base64 strings.
    async function encryptMessage(plaintext, roomKey) {
        const iv         = crypto.getRandomValues(new Uint8Array(12))
        const encoded    = new TextEncoder().encode(plaintext)
        const ciphertext = await crypto.subtle.encrypt(
            { name: 'AES-GCM', iv },
            roomKey,
            encoded,
        )
        return { ciphertext: toBase64(ciphertext), iv: toBase64(iv) }
    }

    // decryptMessage decrypts a base64 ciphertext+iv pair with the room key.
    // Returns the plaintext string.
    async function decryptMessage(b64Ciphertext, b64Iv, roomKey) {
        const plainBytes = await crypto.subtle.decrypt(
            { name: 'AES-GCM', iv: fromBase64(b64Iv) },
            roomKey,
            fromBase64(b64Ciphertext),
        )
        return new TextDecoder().decode(plainBytes)
    }

    return {
        generateOrLoadIdentityKey,
        exportPublicKey,
        importPublicKey,
        generateRoomKey,
        getRoomKey,
        storeRoomKey,
        encryptRoomKeyForUser,
        decryptRoomKey,
        encryptMessage,
        decryptMessage,
    }
})()
