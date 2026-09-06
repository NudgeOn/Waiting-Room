# SPDX-License-Identifier: Apache-2.0
FROM --platform=$BUILDPLATFORM node:24.12.0-bookworm-slim AS ui
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts
COPY apps/admin ./apps/admin
RUN npm run build:admin

FROM --platform=$BUILDPLATFORM golang:1.26.1-bookworm AS runtime
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
COPY deploy ./deploy
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/wr-control ./cmd/wr-control && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/wr-node ./cmd/wr-node

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/NudgeOn/Waiting-Room" \
      org.opencontainers.image.title="NudgeOn Waiting Room" \
      org.opencontainers.image.description="Self-hosted virtual waiting room. Local Preview runtime." \
      org.opencontainers.image.licenses="Apache-2.0" \
      io.nudgeon.waiting-room.runtime-contract="1"
ARG REVISION
ARG VERSION
LABEL org.opencontainers.image.revision=$REVISION org.opencontainers.image.version=$VERSION
COPY --from=runtime /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime /out/wr-control /wr-control
COPY --from=runtime /out/wr-node /wr-node
COPY --from=ui /src/build/admin-ui /ui
USER 65532:65532
ENTRYPOINT ["/wr-control"]
CMD ["serve"]
