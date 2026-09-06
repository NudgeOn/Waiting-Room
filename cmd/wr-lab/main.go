// SPDX-License-Identifier: Apache-2.0
// wr-lab is an explicit loopback-only executable; never package it as a production role.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"waiting-room/internal/lab"
	"waiting-room/internal/queue/model"
	"waiting-room/internal/queue/valkeystore"
)

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run() error {
	address := flag.String("valkey", "127.0.0.1:16379", "isolated local Valkey address")
	quick := flag.Bool("quick", false, "run Quick20 app flow and stop lab HTTP servers")
	templateID := flag.String("template", "calm", "built-in waiting-page template ID")
	hold := flag.Bool("hold", false, "hold admission for local waiting-page inspection")
	port := flag.Int("gateway-port", 18080, "first loopback Gateway port; second uses port+1")
	flag.Parse()
	if *quick && *hold {
		return fmt.Errorf("quick and hold cannot be combined")
	}
	if *port < 1024 || *port > 65534 {
		return fmt.Errorf("gateway-port must be 1024..65534")
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("wr-lab requires a loopback Valkey address")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	config := model.DefaultConfig()
	config.LeaseCap = 3
	config.Rate = 6
	config.AdmissionTTL = 60000
	start, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	store, err := valkeystore.Open(start, *address, fmt.Sprintf("wr:lab:run-%d", time.Now().UnixNano()), config)
	if err != nil {
		return fmt.Errorf("lab Valkey startup failed: %w", err)
	}
	defer store.Close()
	if *hold {
		if _, err := store.Mode(start, "hold"); err != nil {
			return err
		}
	}
	coordinator, err := lab.NewCoordinator(store, config)
	if err != nil {
		return err
	}
	var servers []*http.Server
	defer func() {
		shutdown, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		for _, s := range servers {
			_ = s.Shutdown(shutdown)
		}
	}()
	serve := func(address string, h http.Handler) (string, error) {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			return "", e
		}
		server := &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}
		servers = append(servers, server)
		go func() {
			if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
				log.Print("lab listener stopped")
				cancel()
			}
		}()
		return "http://" + listener.Addr().String(), nil
	}
	origin, err := serve("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"origin": "wr-lab", "path": r.URL.Path})
	}))
	if err != nil {
		return err
	}
	internal, err := serve("127.0.0.1:0", coordinator.Handler())
	if err != nil {
		return err
	}
	returnKey := make([]byte, 32)
	if _, err := rand.Read(returnKey); err != nil {
		return err
	}
	addresses := []string{fmt.Sprintf("127.0.0.1:%d", *port), fmt.Sprintf("127.0.0.1:%d", *port+1)}
	if *quick {
		addresses = []string{"127.0.0.1:0", "127.0.0.1:0"}
	}
	gateways := []string{}
	for _, a := range addresses {
		handler, err := lab.NewGatewayWithReturnKey(internal, origin, coordinator.ServiceKey, coordinator.Public, *templateID, returnKey)
		if err != nil {
			return err
		}
		g, e := serve(a, handler)
		if e != nil {
			return e
		}
		gateways = append(gateways, g)
	}
	pump, cancelPump := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	defer func() { cancelPump(); wg.Wait() }()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pump.Done():
				return
			case <-ticker.C:
				tick, done := context.WithTimeout(pump, 2*time.Second)
				_, _ = store.Promote(tick, 128)
				done()
			}
		}
	}()
	if *quick {
		qctx, done := context.WithTimeout(ctx, 20*time.Second)
		defer done()
		result, e := lab.Quick20(qctx, gateways)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Printf("WEB + APP LOCAL LAB — no Backoffice or production security yet\nGateway 1: %s\nGateway 2: %s\nBrowser: /shop (template=%s). App: POST /_wr/v1/tickets. Ctrl-C stops this lab.\n", gateways[0], gateways[1], *templateID)
	<-ctx.Done()
	return nil
}
