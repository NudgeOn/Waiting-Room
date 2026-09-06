# SPDX-License-Identifier: Apache-2.0
# Build the static Linux binary and UI locally with scripts/local-beta.mjs build.
# No shell/package manager or secret files are included in the image.
FROM scratch
COPY build/local-control/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY build/local-control/wr-control /wr-control
COPY build/local-control/wr-node /wr-node
COPY build/admin-ui /ui
USER 65532:65532
ENTRYPOINT ["/wr-control"]
CMD ["serve"]
