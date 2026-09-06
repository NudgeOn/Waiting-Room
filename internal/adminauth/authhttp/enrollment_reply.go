// SPDX-License-Identifier: Apache-2.0
package authhttp

import "time"

// JSON is the intentional private wire response, never a logging representation.
type enrollmentReply struct {
	Secret  string    `json:"secret"`
	Expires time.Time `json:"expiresAt"`
}

func (enrollmentReply) String() string   { return "[REDACTED_ENROLLMENT_REPLY]" }
func (enrollmentReply) GoString() string { return "[REDACTED_ENROLLMENT_REPLY]" }
