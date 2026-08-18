// Package worker holds Asynq task handlers. Skeleton only for S0 (S0-21) --
// task type sungguhan (email, notifikasi, dll.) ditambahkan mulai S1.
package worker

import "github.com/hibiken/asynq"

// NewMux membuat ServeMux Asynq. Kosong untuk S0 -- handler task ditambahkan
// via mux.HandleFunc(TypeX, HandleX) begitu task pertama diimplementasikan.
func NewMux() *asynq.ServeMux {
	return asynq.NewServeMux()
}
