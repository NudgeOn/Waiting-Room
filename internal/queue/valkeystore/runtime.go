// SPDX-License-Identifier: Apache-2.0
package valkeystore

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	valkey "github.com/valkey-io/valkey-go"
	"waiting-room/internal/control"
	"waiting-room/internal/queue/model"
)

type Metrics struct {
	Waiting            int    `json:"waiting"`
	Ready              int    `json:"ready"`
	Leases             int    `json:"leases"`
	Rate               int    `json:"rate"`
	Mode               string `json:"mode"`
	Revision           int64  `json:"revision"`
	Epoch              uint64 `json:"epoch"`
	RecoveryUntil      int64  `json:"recoveryUntil"`
	RecoveryFence      uint64 `json:"recoveryFence"`
	RecoveryReason     string `json:"recoveryReason"`
	RecoveryValidation string `json:"recoveryValidation"`
}

// InstallRuntimeLibrary is an explicit deployment operation using an owner
// connection. A serving Coordinator can only inspect the immutable library.
func InstallRuntimeLibrary(ctx context.Context, client valkey.Client) error {
	err := client.Do(ctx, client.B().FunctionLoad().FunctionCode(runtimeLibraryV4).Build()).Error()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return verifyRuntimeLibrary(ctx, client)
}
func verifyRuntimeLibrary(ctx context.Context, c valkey.Client) error {
	entries, err := c.Do(ctx, c.B().FunctionList().Libraryname("wr_queue_runtime_v4").Withcode().Build()).ToArray()
	if err != nil {
		return err
	}
	if len(entries) != 1 {
		return ErrSchema
	}
	fields, err := entries[0].AsMap()
	if err != nil {
		return err
	}
	code := fields["library_code"]
	actual, err := code.ToString()
	if err != nil || actual != runtimeLibraryV4 {
		return ErrSchema
	}
	return nil
}
func OpenRuntimeRoom(ctx context.Context, options valkey.ClientOption, namespace, room string, c model.Config, installation InstallationConfig) (*Store, error) {
	return openRuntimeRoom(ctx, options, namespace, room, c, installation, 4, 1, 0)
}

// OpenRecoveryRoom uses a versioned epoch namespace. The prior namespace is
// retained for backup/inspection and is never silently reset or reinterpreted.
func OpenRecoveryRoom(ctx context.Context, options valkey.ClientOption, namespace, room string, c model.Config, installation InstallationConfig, epoch uint64, notBefore int64) (*Store, error) {
	return openRuntimeRoom(ctx, options, namespace, room, c, installation, 5, epoch, notBefore)
}
func (s *Store) runtimeFunction(operation string) string {
	if s.runtimeVersion == 5 {
		return "wr_r5_" + operation
	}
	return "wr_r4_" + operation
}
func InstallRecoveryLibrary(ctx context.Context, client valkey.Client) error {
	err := client.Do(ctx, client.B().FunctionLoad().FunctionCode(runtimeLibraryV5).Build()).Error()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return verifyRecoveryLibrary(ctx, client)
}
func verifyRecoveryLibrary(ctx context.Context, c valkey.Client) error {
	entries, err := c.Do(ctx, c.B().FunctionList().Libraryname("wr_queue_runtime_v5").Withcode().Build()).ToArray()
	if err != nil || len(entries) != 1 {
		return ErrSchema
	}
	fields, err := entries[0].AsMap()
	if err != nil {
		return ErrSchema
	}
	code := fields["library_code"]
	actual, err := code.ToString()
	if err != nil || actual != runtimeLibraryV5 {
		return ErrSchema
	}
	return nil
}
func openRuntimeRoom(ctx context.Context, options valkey.ClientOption, namespace, room string, c model.Config, installation InstallationConfig, version int, epoch uint64, notBefore int64) (*Store, error) {
	if !regexp.MustCompile(`^wr:(runtime|lab):[a-zA-Z0-9_-]{1,80}$`).MatchString(namespace) || !regexp.MustCompile(`^[a-z2-7]{20}$`).MatchString(room) {
		return nil, ErrSchema
	}
	if err := installation.validate(c); err != nil {
		return nil, err
	}
	if epoch < 1 || epoch >= 9007199254740990 || notBefore < 0 || notBefore >= 9007199254740990 {
		return nil, ErrSchema
	}
	baseNamespace := namespace
	if epoch > 1 {
		namespace += fmt.Sprintf("-epoch-%d", epoch)
	}
	options.ForceSingleClient = true
	options.DisableCache = true
	options.DisableRetry = true
	client, err := valkey.NewClient(options)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			client.Close()
		}
	}()
	cluster, err := client.Do(ctx, client.B().Info().Section("cluster").Build()).ToString()
	if err != nil || !strings.Contains(cluster, "cluster_enabled:0\r\n") {
		return nil, ErrSchema
	}
	verify := verifyRuntimeLibrary
	if version == 5 {
		verify = verifyRecoveryLibrary
	}
	if err = verify(ctx, client); err != nil {
		return nil, err
	}
	s := &Store{client: client, installation: true, runtime: true, runtimeVersion: version, epoch: epoch}
	for _, suffix := range []string{"meta", "tickets", "waiting", "expiry", "leases", "rate", "idempotency", "idem-expiry"} {
		s.keys = append(s.keys, namespace+"{"+room+":1}:"+suffix)
	}
	for _, suffix := range []string{"meta", "visitors", "idempotency"} {
		s.keys = append(s.keys, namespace+"{installation:1}:"+suffix)
	}
	if version == 5 {
		s.keys = append(s.keys, baseNamespace+"{epoch:1}:meta")
	}
	s.primary, err = s.primaryID(ctx)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(c)
	global, _ := json.Marshal(installation)
	args := []string{string(data), s.primary, string(global), room}
	if version == 5 {
		args = append(args, fmt.Sprint(epoch), fmt.Sprint(notBefore))
	}
	if err = client.Do(ctx, client.B().Fcall().Function(s.runtimeFunction("init")).Numkeys(int64(len(s.keys))).Key(s.keys...).Arg(args...).Build()).Error(); err != nil {
		return nil, classify(err)
	}
	if _, err = s.MaintainRecovery(ctx); err != nil {
		return nil, err
	}
	success = true
	return s, nil
}
func (s *Store) Configure(ctx context.Context, c model.Config, configRevision int64, runtime control.Runtime) (Result, error) {
	if !s.runtime || configRevision < 0 || runtime.Revision < 1 || runtime.Epoch != s.epoch {
		return Result{}, ErrSchema
	}
	if _, err := model.New(c); err != nil {
		return Result{}, err
	}
	raw, _ := json.Marshal(c)
	return s.call(ctx, false, "configure", string(raw), fmt.Sprint(configRevision), fmt.Sprint(runtime.Revision), runtime.Mode)
}
func (s *Store) Metrics(ctx context.Context) (Result, error) {
	if !s.runtime {
		return Result{}, ErrSchema
	}
	return s.call(ctx, true, "metrics")
}
