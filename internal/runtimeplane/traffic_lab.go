// SPDX-License-Identifier: Apache-2.0
package runtimeplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
	"waiting-room/internal/adminauth/pgstore"
	"waiting-room/internal/control"
	"waiting-room/internal/localcontrol"
	"waiting-room/internal/trafficlab"
)

func serveTraffic(execute trafficlab.Execute, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if execute == nil || Peer(r) != "control" || r.Host != "coordinator:19446" {
		w.WriteHeader(403)
		return
	}
	if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.EscapedPath() != r.URL.Path || r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(400)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	var in trafficlab.Input
	if err != nil || control.DecodeExact(raw, &in) != nil || !pgstore.LabRunID.MatchString(in.RunID) || trafficlab.Visitors(in.Preset) == 0 {
		w.WriteHeader(400)
		return
	}
	rc := http.NewResponseController(w)
	if rc.SetReadDeadline(time.Time{}) != nil || rc.SetWriteDeadline(time.Now().Add(trafficlab.MaxDuration+5*time.Second)) != nil {
		w.WriteHeader(503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), trafficlab.MaxDuration)
	defer cancel()
	w.Header().Set("Content-Type", "application/x-ndjson")
	encoder := json.NewEncoder(w)
	emit := func(report trafficlab.Report) error {
		if encoder.Encode(report) != nil {
			return trafficlab.ErrRun
		}
		return rc.Flush()
	}
	report, err := execute(ctx, in, emit)
	if !report.Valid(in.Preset) || report.State == "running" {
		report = trafficlab.NewReport(in.Preset)
		report.State = "interrupted"
		report.Stage = "finished"
		now := time.Now().UTC()
		report.FinishedAt = &now
	}
	if err != nil && report.State == "passed" {
		report.State = "interrupted"
	}
	_ = emit(report)
}

// TrafficExecutor sends no admin session/CSRF/URL to the data role. Only the
// Control mTLS identity can select one of the two bounded, fixed sample presets.
func TrafficExecutor(identity localcontrol.NodeIdentity) (trafficlab.Execute, func(), error) {
	if identity.Node != "control" {
		return nil, nil, trafficlab.ErrRun
	}
	tr, err := internalTransport(identity)
	if err != nil {
		return nil, nil, err
	}
	client := &http.Client{Transport: tr, Timeout: trafficlab.MaxDuration + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	execute := func(ctx context.Context, in trafficlab.Input, emit func(trafficlab.Report) error) (trafficlab.Report, error) {
		raw, _ := json.Marshal(in)
		req, err := http.NewRequestWithContext(ctx, "POST", "https://coordinator:19446/internal/v1/traffic-lab", bytes.NewReader(raw))
		if err != nil {
			return trafficlab.Report{}, trafficlab.ErrRun
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return trafficlab.Report{}, trafficlab.ErrRun
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/x-ndjson" {
			return trafficlab.Report{}, trafficlab.ErrRun
		}
		decoder := json.NewDecoder(io.LimitReader(res.Body, 1024*1024))
		var last trafficlab.Report
		for i := 0; i < 20; i++ {
			var report trafficlab.Report
			if decoder.Decode(&report) != nil || !report.Valid(in.Preset) {
				return last, trafficlab.ErrRun
			}
			last = report
			if report.State != "running" {
				return report, nil
			}
			if emit != nil && emit(report) != nil {
				return last, trafficlab.ErrRun
			}
		}
		return last, trafficlab.ErrRun
	}
	return execute, tr.CloseIdleConnections, nil
}
