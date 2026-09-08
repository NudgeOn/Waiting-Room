// SPDX-License-Identifier: Apache-2.0
package lab

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"errors"
	"net/http"
	"regexp"
	"time"
	"waiting-room/internal/queue/model"
)

// Binding is supplied only from a fully verified installation configuration.
// The legacy lab constructors retain isolated defaults for regression tests.
type Binding struct {
	Room, Audience, Kid string
	Epoch               uint64
	ValidTarget         func(string) bool
}

func labBinding() Binding {
	return Binding{Room: Room, Audience: Audience, Kid: "lab-ephemeral", Epoch: 1, ValidTarget: ValidTarget}
}
func (b Binding) base() string { return "/_wr/v1/rooms/" + b.Room }
func (b Binding) validate() error {
	if !regexp.MustCompile(`^[a-z2-7]{20}$`).MatchString(b.Room) || b.Epoch < 1 || b.ValidTarget == nil || len(b.Audience) == 0 || len(b.Audience) > 256 || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(b.Kid) {
		return errors.New("invalid runtime binding")
	}
	return nil
}
func NewBoundCoordinator(q Queue, c model.Config, b Binding, key ed25519.PrivateKey, replayKey []byte, service string) (*Coordinator, error) {
	if b.validate() != nil || len(key) != ed25519.PrivateKeySize || len(replayKey) != 32 || len(service) < 32 || c.ClockSkew != 30000 {
		return nil, errors.New("invalid runtime keys or binding")
	}
	if _, err := model.New(c); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(replayKey)
	if err != nil {
		return nil, err
	}
	replay, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	private := append(ed25519.PrivateKey(nil), key...)
	return &Coordinator{queue: q, config: c, binding: b, private: private, Public: private.Public().(ed25519.PublicKey), ServiceKey: service, replay: replay}, nil
}
func (b *browserGateway) queueCookie() string {
	if b.secure {
		return "__Host-wrq_" + b.binding.Room
	}
	return "wr_dev_q_" + b.binding.Room
}
func (b *browserGateway) admissionCookie() string {
	if b.secure {
		return "__Host-wra_" + b.binding.Room
	}
	return "wr_dev_a_" + b.binding.Room
}
func (b *browserGateway) returnCookie() string {
	if b.secure {
		return "__Host-wrr_" + b.binding.Room
	}
	return "wr_dev_r_" + b.binding.Room
}
func (b *browserGateway) waitPath() string { return "/_wr/wait/" + b.binding.Room }
func (b *browserGateway) scheme() string {
	if b.secure {
		return "https"
	}
	return "http"
}
func (b *browserGateway) setCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: b.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires})
}
