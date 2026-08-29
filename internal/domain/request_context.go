package domain

// requestMetaKey -- tipe khusus (bukan string) supaya key context.Value ini
// tidak bisa bentrok dengan key manapun dari package lain (idiom Go
// standar). Tetap berfungsi lintas fasthttp.RequestCtx karena
// RequestCtx.Value(key any) meneruskan langsung ke UserValue(key any) --
// tidak dibatasi key bertipe string.
type requestMetaKey struct{}

// RequestMetaKey dipakai middleware.RequestMeta untuk menyuntikkan info
// asal HTTP request (IP, method+path) ke context.Context, dan
// account_repository.go (logAudit dkk.) untuk membacanya kembali saat
// menulis audit trail (2026-08-29, permintaan user: audit trail perlu
// info asal request) -- diletakkan di domain (bukan middleware atau
// repository) supaya kedua package bisa memakainya tanpa import cycle.
var RequestMetaKey = requestMetaKey{}

// RequestMeta -- info request HTTP aktif untuk audit trail. Path berformat
// "METHOD /path", mis. "POST /platform/group-admins/:id".
type RequestMeta struct {
	IP   string
	Path string
}
