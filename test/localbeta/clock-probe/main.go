//go:build ignore

// SPDX-License-Identifier: Apache-2.0
// Protocol verification in a disposable fixture. Private identity stays in memory.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"waiting-room/internal/configtrust"
	"waiting-room/internal/control"
	"waiting-room/internal/localcontrol"
)

func main() {
	if run() != nil {
		fmt.Println(`{"error":"clock_protocol_failed","details":"suppressed"}`)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return errors.New("role")
	}
	role := os.Args[1]
	n, err := localcontrol.LoadIdentity("/identity", role)
	if err != nil {
		return err
	}
	tls, err := n.TLS(true)
	if err != nil {
		return err
	}
	tls.ServerName = "control"
	tr := &http.Transport{TLSClientConfig: tls, Proxy: nil, MaxIdleConnsPerHost: 16}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path, host string, body []byte) (int, []byte, error) {
		r, e := http.NewRequest(method, "https://control:19445"+path, bytes.NewReader(body))
		if e != nil {
			return 0, nil, e
		}
		if host != "" {
			r.Host = host
		}
		if method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		out, e := c.Do(r)
		if e != nil {
			return 0, nil, e
		}
		defer out.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(out.Body, 65537))
		return out.StatusCode, raw, e
	}
	code, before, err := call("GET", "/internal/v1/config", "", nil)
	if err != nil {
		return err
	}
	if role == "demo-origin" {
		if code != 403 {
			return errors.New("origin allowed")
		}
		code, _, err = call("POST", "/internal/v1/config/clock-recovery", "", []byte(`{}`))
		if err != nil || code != 403 {
			return errors.New("origin refresh allowed")
		}
		fmt.Println(`{"peer":"demo-origin","configStatus":403,"refreshStatus":403}`)
		return nil
	}
	if role != "gateway" || code != 200 {
		return errors.New("gateway unavailable")
	}
	gate, err := configtrust.Open(map[string]ed25519.PublicKey{"config-v1": n.ConfigPublic}, n.Installation, 1, control.ValidateDelivery, &configtrust.MemoryStore{})
	if err != nil || gate.Apply(before) != nil {
		return errors.New("signature")
	}
	old, err := gate.Current()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(before)
	in := configtrust.ClockRecovery{Generation: old.Generation, NotBefore: time.Now().Unix() + 1, Digest: hex.EncodeToString(sum[:])}
	bad := in
	bad.NotBefore += 3600
	raw, _ := json.Marshal(bad)
	code, _, err = call("POST", "/internal/v1/config/clock-recovery", "", raw)
	if err != nil || code != 409 {
		return errors.New("future accepted")
	}
	code, _, err = call("POST", "/internal/v1/config/clock-recovery", "evil:19445", raw)
	if err != nil || code != 400 {
		return errors.New("authority accepted")
	}
	code, _, err = call("POST", "/internal/v1/config/clock-recovery", "", []byte(`{"generation":1,"notBefore":1,"digest":"bad","mode":"OFF"}`))
	if err != nil || code != 400 {
		return errors.New("extra field accepted")
	}
	if wait := time.Until(time.Unix(in.NotBefore, 0)); wait > 0 {
		time.Sleep(wait + time.Millisecond)
	}
	raw, _ = json.Marshal(in)
	responses := make([][]byte, 8)
	errs := make([]error, 8)
	statuses := make([]int, 8)
	var wg sync.WaitGroup
	started := time.Now()
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], responses[i], errs[i] = call("POST", "/internal/v1/config/clock-recovery", "", raw)
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil || statuses[i] != 200 || !bytes.Equal(responses[0], responses[i]) {
			return errors.New("replay")
		}
	}
	if gate.Apply(responses[0]) != nil {
		return errors.New("new signature")
	}
	current, err := gate.Current()
	if err != nil || current.Generation != old.Generation+1 || current.IssuedAt < in.NotBefore || !bytes.Equal(current.Payload, old.Payload) {
		return errors.New("approved state")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"peer": "gateway", "beforeGeneration": old.Generation, "afterGeneration": current.Generation, "concurrentRequests": 8, "sameSignedBytes": true, "sameApprovedPayload": true, "futureBoundaryStatus": 409, "wrongAuthorityStatus": 400, "unknownFieldStatus": 400, "elapsedMs": time.Since(started).Milliseconds()})
}
