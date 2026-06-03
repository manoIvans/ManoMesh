package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// Token primitives compartilhadas entre password reset, email
// verification e refresh tokens.
//
// O token retornado por Generate() é o que viaja pro usuário (via
// email pros dois primeiros, via response do login pro refresh).
// O DB guarda APENAS o Hash do token — vazamento do DB não expõe
// tokens utilizáveis.
//
// Tamanho: 32 bytes (256 bits) de entropia, codificado em hex (64
// chars). Suficiente contra brute force; pequeno o bastante pra
// caber em URL sem encoding extra.

const tokenBytes = 32

// GenerateToken devolve (rawToken, hash) usando crypto/rand.
//
//	rawToken: o que o caller MANDA pro usuário (URL, email body…).
//	hash:     o que o caller PERSISTE em token_hash do DB.
//
// Erro só sai se crypto/rand falhar — situação de OS quebrado,
// caller deve propagar como 500.
func GenerateToken() (rawToken, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("rand bytes: %w", err)
	}
	raw := hex.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// HashToken aplica SHA-256 ao token recebido (login do user no
// /reset?token=...) e devolve o hex pra LOOKUP em token_hash do DB.
//
// SHA-256 simples (sem salt) é OK aqui porque:
//   - O token original tem 256 bits de entropia (não é uma senha).
//   - Comparar com hash dificulta apenas dump do DB; um atacante com
//     o token raw em mãos já passou pela barreira.
func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeCompare é wrapper sobre subtle.ConstantTimeCompare —
// usado quando comparamos hashes em memória (não via WHERE no SQL).
// Devolve true se iguais.
func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
