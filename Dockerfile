# Optional packaging; requires Docker/BuildKit and a trusted enterprise deployment pipeline.
# Not executed in the current Linux test environment because Docker is unavailable.
FROM scratch
ARG TARGETARCH
COPY core/alloc_linux_${TARGETARCH} /alloc
WORKDIR /work
USER 65532:65532
ENTRYPOINT ["/alloc"]
