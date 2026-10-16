package service

import (
	"reflect"
	"testing"

	"github.com/mtaaufaan/prodo-backend/internal/repository"
)

func TestParseMentionTokens(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"kosong", "tanpa mention", nil},
		{"dua mention unik, huruf kecil", "cek @Eka dan @budi.s lalu @eka lagi", []string{"eka", "budi.s"}},
		{"email bukan mention", "kirim ke a@b.com dan @citra", []string{"citra"}},
		{"titik akhir kalimat dibuang", "terima kasih @dewi.", []string{"dewi"}},
		{"di awal teks dan setelah baris baru", "@andi\nhalo @bayu", []string{"andi", "bayu"}},
		{"harus diawali huruf", "@123 dan @_x", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseMentionTokens(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveMentions(t *testing.T) {
	cands := []repository.MentionCandidate{
		{UserID: "1", DisplayName: "Eka Editor", Email: "eka.editor@prodo.local"},
		{UserID: "2", DisplayName: "Budi Santoso", Email: "budi@prodo.local"},
		{UserID: "3", DisplayName: "Budi Hartono", Email: "hartono@prodo.local"},
		{UserID: "4", DisplayName: "Citra", Email: "citra@prodo.local"},
	}
	ids := func(in []repository.MentionCandidate) []string {
		var out []string
		for _, c := range in {
			out = append(out, c.UserID)
		}
		return out
	}
	t.Run("email, nama tanpa spasi, nama dengan titik", func(t *testing.T) {
		got := ids(ResolveMentions([]string{"eka.editor", "budisantoso", "citra"}, cands))
		if !reflect.DeepEqual(got, []string{"1", "2", "4"}) {
			t.Fatalf("got %v", got)
		}
		if got := ids(ResolveMentions([]string{"budi.hartono"}, cands)); !reflect.DeepEqual(got, []string{"3"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("token ambigu atau tak dikenal diabaikan", func(t *testing.T) {
		// "budi" = bagian email user 2 saja -> cocok; tambahkan user yang juga "budi" agar ambigu.
		amb := append([]repository.MentionCandidate{}, cands...)
		amb = append(amb, repository.MentionCandidate{UserID: "5", DisplayName: "Budi", Email: "x@prodo.local"})
		if got := ResolveMentions([]string{"budi", "tidakada"}, amb); len(got) != 0 {
			t.Fatalf("diharapkan kosong, got %v", got)
		}
	})
	t.Run("user yang sama dari dua token hanya sekali", func(t *testing.T) {
		got := ids(ResolveMentions([]string{"eka.editor", "ekaeditor"}, cands))
		if !reflect.DeepEqual(got, []string{"1"}) {
			t.Fatalf("got %v", got)
		}
	})
}
