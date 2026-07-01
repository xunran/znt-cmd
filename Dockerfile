ARG GO_BASE_IMAGE=golang:1.22-bookworm
FROM ${GO_BASE_IMAGE} AS build

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

RUN test -s /etc/ssl/certs/ca-certificates.crt

WORKDIR /src
COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY migrations ./migrations
COPY eval ./eval
COPY docs/openapi.clean-core.v1.json ./docs/openapi.clean-core.v1.json
COPY docs/e2e_regression_matrix.md ./docs/e2e_regression_matrix.md
RUN go test ./...
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/clean-core-server ./cmd/clean-core-server

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/clean-core-server /clean-core-server
COPY --from=build /src/migrations /migrations
COPY --from=build /src/docs/openapi.clean-core.v1.json /docs/openapi.clean-core.v1.json
COPY --from=build /src/docs/e2e_regression_matrix.md /docs/e2e_regression_matrix.md
WORKDIR /
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/clean-core-server"]
