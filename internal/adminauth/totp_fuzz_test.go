// SPDX-License-Identifier: Apache-2.0
package adminauth

import (
	"context"
	"encoding/base32"
	"testing"
	"time"
)

func FuzzTOTPInput(f *testing.F) {
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(vectorKey))
	f.Add(encoded, "287082", int64(59))
	f.Add(encoded, "359152", int64(60))
	f.Add("", "", int64(-1))
	f.Add(encoded, "１２３４５６", int64(20000000000))
	f.Fuzz(func(t *testing.T, raw, code string, seconds int64) {
		secret, err := ParseSecret(raw)
		if err != nil {
			return
		}
		store := newCounterFixture()
		ref := CredentialRef{UserID: "user-one", Version: 1}
		err = VerifyAndConsume(context.Background(), ref, secret, code, time.Unix(seconds, 0), store)
		if err == nil {
			if len(code) != 6 || seconds < 0 || store.calls != 1 {
				t.Fatal("invalid success")
			}
			if VerifyAndConsume(context.Background(), ref, secret, code, time.Unix(seconds, 0), store) != ErrInvalidOrReplayed {
				t.Fatal("fuzzed replay accepted")
			}
		} else if store.calls != 0 {
			t.Fatal("invalid match touched counter")
		}
	})
}
