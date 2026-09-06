// SPDX-License-Identifier: Apache-2.0
package deploy

import _ "embed"

// PreviewCompose is a complete runtime definition; an installed wrctl does not
// need a source checkout, Node, Go or a local image build.
//
//go:embed compose/preview.yaml
var PreviewCompose []byte
