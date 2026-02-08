package tapo

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"time"
)

const (
	klapSeedSize = 16
	klapIVSize   = 12
)

// klapSession holds the encryption state for a KLAP session.
type klapSession struct {
	localSeed  []byte
	remoteSeed []byte
	authHash   []byte
	key        []byte
	ivSeq      []byte
	sig        []byte
	seq        int32
	httpClient *http.Client
	baseURL    string
	host       string
}

// newKlapSession creates a new KLAP session for the given device IP.
func newKlapSession(ip, username, password string) (*klapSession, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
	}

	localSeed := make([]byte, klapSeedSize)
	if _, err := rand.Read(localSeed); err != nil {
		return nil, fmt.Errorf("failed to generate local seed: %w", err)
	}

	authHash := generateAuthHash(username, password)

	session := &klapSession{
		localSeed:  localSeed,
		authHash:   authHash,
		httpClient: client,
		baseURL:    fmt.Sprintf("http://%s/app", ip),
		host:       ip,
	}

	return session, nil
}

// generateAuthHash creates the authentication hash from credentials.
// authHash = SHA256(SHA1(username) + SHA1(password))
func generateAuthHash(username, password string) []byte {
	userHash := sha1.Sum([]byte(username))
	passHash := sha1.Sum([]byte(password))

	combined := make([]byte, 0, sha1.Size*2)
	combined = append(combined, userHash[:]...)
	combined = append(combined, passHash[:]...)

	authHash := sha256.Sum256(combined)
	return authHash[:]
}

// handshake performs the KLAP handshake to establish encryption keys.
func (s *klapSession) handshake() error {
	// Handshake 1: Send local seed, receive remote seed
	if err := s.handshake1(); err != nil {
		return fmt.Errorf("handshake1 failed: %w", err)
	}

	// Handshake 2: Complete authentication
	if err := s.handshake2(); err != nil {
		return fmt.Errorf("handshake2 failed: %w", err)
	}

	// Derive encryption keys
	s.deriveKeys()

	return nil
}

// handshake1 performs the first step of the KLAP handshake.
func (s *klapSession) handshake1() error {
	reqURL := s.baseURL + "/handshake1"

	resp, err := s.httpClient.Post(reqURL, "application/octet-stream", bytes.NewReader(s.localSeed))
	if err != nil {
		return fmt.Errorf("POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("handshake1 returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Response format: remote_seed (16 bytes) + server_hash (32 bytes)
	if len(body) != 48 {
		return fmt.Errorf("unexpected handshake1 response length: %d", len(body))
	}

	s.remoteSeed = body[:16]
	serverHash := body[16:48]

	// Verify server hash: SHA256(local_seed + remote_seed + auth_hash)
	expectedHash := s.calculateServerHash()
	if !bytes.Equal(serverHash, expectedHash) {
		return fmt.Errorf("server hash verification failed (invalid credentials?)")
	}

	return nil
}

// handshake2 performs the second step of the KLAP handshake.
func (s *klapSession) handshake2() error {
	reqURL := s.baseURL + "/handshake2"

	// Client hash: SHA256(remote_seed + local_seed + auth_hash)
	clientHash := s.calculateClientHash()

	resp, err := s.httpClient.Post(reqURL, "application/octet-stream", bytes.NewReader(clientHash))
	if err != nil {
		return fmt.Errorf("POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("handshake2 returned status %d", resp.StatusCode)
	}

	return nil
}

// calculateServerHash calculates the expected server hash.
func (s *klapSession) calculateServerHash() []byte {
	h := sha256.New()
	h.Write(s.localSeed)
	h.Write(s.remoteSeed)
	h.Write(s.authHash)
	return h.Sum(nil)
}

// calculateClientHash calculates the client hash for handshake2.
func (s *klapSession) calculateClientHash() []byte {
	h := sha256.New()
	h.Write(s.remoteSeed)
	h.Write(s.localSeed)
	h.Write(s.authHash)
	return h.Sum(nil)
}

// deriveKeys derives encryption keys from the handshake data.
func (s *klapSession) deriveKeys() {
	// local_hash = local_seed + remote_seed + auth_hash
	localHash := concat(s.localSeed, s.remoteSeed, s.authHash)

	// Key derivation: SHA256("lsk" + local_hash)[:16]
	keyData := sha256Hash([]byte("lsk"), localHash)
	s.key = keyData[:16]

	// IV seed derivation: SHA256("iv" + local_hash)[:12]
	// Seq derivation: SHA256("iv" + local_hash)[28:32] as big-endian i32
	ivData := sha256Hash([]byte("iv"), localHash)
	s.ivSeq = ivData[:klapIVSize]
	s.seq = int32(binary.BigEndian.Uint32(ivData[28:32]))

	// Signature derivation: SHA256("ldk" + local_hash)[:28]
	sigData := sha256Hash([]byte("ldk"), localHash)
	s.sig = sigData[:28]
}

// concat concatenates multiple byte slices.
func concat(slices ...[]byte) []byte {
	var total int
	for _, s := range slices {
		total += len(s)
	}
	result := make([]byte, 0, total)
	for _, s := range slices {
		result = append(result, s...)
	}
	return result
}

// sha256Hash computes SHA256 of concatenated byte slices.
func sha256Hash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// encrypt encrypts a plaintext payload for transmission.
func (s *klapSession) encrypt(plaintext []byte) ([]byte, int32, error) {
	s.seq++
	seq := s.seq

	// Build 16-byte IV: ivSeq (12 bytes) + seq (4 bytes big-endian)
	iv := make([]byte, 16)
	copy(iv, s.ivSeq)
	binary.BigEndian.PutUint32(iv[12:], uint32(seq))

	// Pad plaintext to AES block size
	padded := pkcs7Pad(plaintext, aes.BlockSize)

	// Encrypt with AES-CBC
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)

	// Calculate signature: SHA256(sig + seq_bytes + ciphertext)
	seqBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(seqBytes, uint32(seq))

	sigHash := sha256.New()
	sigHash.Write(s.sig)
	sigHash.Write(seqBytes)
	sigHash.Write(ciphertext)
	signature := sigHash.Sum(nil)

	// Final payload: signature (32 bytes) + ciphertext
	result := make([]byte, 0, 32+len(ciphertext))
	result = append(result, signature...)
	result = append(result, ciphertext...)

	return result, seq, nil
}

// decrypt decrypts a received payload.
func (s *klapSession) decrypt(payload []byte, seq int32) ([]byte, error) {
	if len(payload) < 32 {
		return nil, fmt.Errorf("payload too short")
	}

	// Split signature and ciphertext
	signature := payload[:32]
	ciphertext := payload[32:]

	// Verify signature
	seqBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(seqBytes, uint32(seq))

	sigHash := sha256.New()
	sigHash.Write(s.sig)
	sigHash.Write(seqBytes)
	sigHash.Write(ciphertext)
	expectedSig := sigHash.Sum(nil)

	if !bytes.Equal(signature, expectedSig) {
		return nil, fmt.Errorf("signature verification failed")
	}

	// Build 16-byte IV: ivSeq (12 bytes) + seq (4 bytes big-endian)
	iv := make([]byte, 16)
	copy(iv, s.ivSeq)
	binary.BigEndian.PutUint32(iv[12:], uint32(seq))

	// Decrypt with AES-CBC
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding
	plaintext, err = pkcs7Unpad(plaintext)
	if err != nil {
		return nil, fmt.Errorf("failed to unpad: %w", err)
	}

	return plaintext, nil
}

// request sends an encrypted request and decrypts the response.
func (s *klapSession) request(payload []byte) ([]byte, error) {
	encrypted, seq, err := s.encrypt(payload)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	reqURL := fmt.Sprintf("%s/request?seq=%d", s.baseURL, seq)

	resp, err := s.httpClient.Post(reqURL, "application/octet-stream", bytes.NewReader(encrypted))
	if err != nil {
		return nil, fmt.Errorf("POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	plaintext, err := s.decrypt(body, seq)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// pkcs7Pad pads the data to the specified block size using PKCS7.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

// pkcs7Unpad removes PKCS7 padding from the data.
func pkcs7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, fmt.Errorf("empty data")
	}

	padding := int(data[length-1])
	if padding > length || padding == 0 {
		return nil, fmt.Errorf("invalid padding")
	}

	for i := 0; i < padding; i++ {
		if data[length-1-i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding")
		}
	}

	return data[:length-padding], nil
}
