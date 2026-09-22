# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
      -o /out/rightsizer ./cmd/rightsizer \
 && mkdir -p /out/data

FROM scratch
LABEL org.opencontainers.image.title="rightsizer" \
      org.opencontainers.image.description="Read-only vSphere rightsizing analysis" \
      org.opencontainers.image.authors="Marco Colombo <https://marco.wf>" \
      org.opencontainers.image.source="https://github.com/MarcoColomb0/rightsizer" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/rightsizer /rightsizer
COPY --from=build --chown=65532:65532 --chmod=700 /out/data /data
ENV RIGHTSIZER_DATA=/data PATH=/
VOLUME /data
EXPOSE 8443
USER 65532:65532
HEALTHCHECK --interval=1m --timeout=5s CMD ["/rightsizer", "status"]
ENTRYPOINT ["/rightsizer"]
CMD ["daemon"]
