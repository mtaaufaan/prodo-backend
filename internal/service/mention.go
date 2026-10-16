package service

import (
	"regexp"
	"strings"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

// Parser dan resolusi @mention komentar (S6E-04, US-030) -- fungsi murni.

// mentionRe -- "@nama" yang didahului awal teks atau karakter bukan [\w.] (agar "a@b.com" bukan
// mention). Token: huruf di awal lalu huruf/angka/_/. ; titik di ujung (akhir kalimat) dibuang.
var mentionRe = regexp.MustCompile(`(?:^|[^\w.])@([A-Za-z][\w.]*)`)

// ParseMentionTokens mengembalikan token unik (huruf kecil) sesuai urutan kemunculan.
func ParseMentionTokens(text string) []string {
	seen := map[string]bool{}
	var tokens []string
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		tok := strings.ToLower(strings.TrimRight(m[1], "."))
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		tokens = append(tokens, tok)
	}
	return tokens
}

// candidateKeys -- nama yang boleh dipakai menyebut kandidat: bagian email sebelum "@", nama tampil
// tanpa spasi ("ekaeditor"), dan nama tampil dengan titik ("eka.editor"), semuanya huruf kecil.
func candidateKeys(c repository.MentionCandidate) []string {
	var keys []string
	if i := strings.Index(c.Email, "@"); i > 0 {
		keys = append(keys, strings.ToLower(c.Email[:i]))
	}
	if name := strings.ToLower(strings.TrimSpace(c.DisplayName)); name != "" {
		fields := strings.Fields(name)
		keys = append(keys, strings.Join(fields, ""), strings.Join(fields, "."))
	}
	return keys
}

// ResolveMentions memetakan token ke kandidat. Token yang cocok dengan LEBIH DARI SATU user
// (ambigu) atau tidak cocok siapa pun diabaikan. Hasil tanpa duplikat user.
func ResolveMentions(tokens []string, candidates []repository.MentionCandidate) []repository.MentionCandidate {
	byKey := map[string]map[string]repository.MentionCandidate{}
	for _, c := range candidates {
		for _, k := range candidateKeys(c) {
			if byKey[k] == nil {
				byKey[k] = map[string]repository.MentionCandidate{}
			}
			byKey[k][c.UserID] = c
		}
	}
	var out []repository.MentionCandidate
	seen := map[string]bool{}
	for _, tok := range tokens {
		users := byKey[tok]
		if len(users) != 1 {
			continue
		}
		for id, c := range users {
			if !seen[id] {
				seen[id] = true
				out = append(out, c)
			}
		}
	}
	return out
}
