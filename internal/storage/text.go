package storage

import (
	"bytes"
	"unicode/utf8"
)

// maxBodyText caps the searchable mirror of a body.
//
// A 1 MiB body would produce roughly a million trigram entries in the GIN index: the index
// would dwarf the data and make inserts slow, while nobody searches for a phrase at byte
// 900000 of a webhook payload.
const maxBodyText = 64 << 10

// SearchableText returns the part of the body that can be searched, or "" when it cannot
// be represented as text at all.
//
// The two rules mirror what PostgreSQL would accept:
//   - the body must be valid UTF-8 (convert_from raises 22021 otherwise);
//   - it must not contain NUL (PostgreSQL text cannot store \0).
//
// Both drivers (PostgreSQL and the in-memory one) must use this exact function: if the
// memory driver were more permissive, a regression like "binary body breaks keyword
// search" could never be reproduced by a unit test.
func SearchableText(body []byte) string {
	if len(body) == 0 || !utf8.Valid(body) || bytes.Contains(body, []byte{0}) {
		return ""
	}

	s := string(body)
	if len(s) <= maxBodyText {
		return s
	}

	// Cut on a rune boundary: never leave a half encoded character behind.
	s = s[:maxBodyText]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}

	return s
}
