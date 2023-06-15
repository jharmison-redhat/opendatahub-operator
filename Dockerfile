# Build the manager binary
ARG GOLANG_VERSION=1.18.9
ARG LOCAL_BUNDLE=odh-manifests

FROM registry.access.redhat.com/ubi8/go-toolset:$GOLANG_VERSION as builder

WORKDIR /workspace
USER root
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY apis/ apis/
COPY controllers/ controllers/
COPY $LOCAL_BUNDLE/ odh-manifests/

# Add in the odh-manifests tarball
RUN mkdir -p /opt/manifests &&\
    tar -czf /opt/manifests/odh-manifests.tar.gz \
        --exclude={.*,*.md,Makefile,Dockerfile,Containerfile,OWNERS,tests} \
        odh-manifests

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go


FROM registry.access.redhat.com/ubi8/ubi-minimal:latest
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /opt/manifests/odh-manifests.tar.gz /opt/manifests/

RUN chown -R 1001:0 /opt/manifests &&\
    chmod -R a+r /opt/manifests

USER 1001

ENTRYPOINT ["/manager"]
