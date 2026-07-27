FROM scratch
ARG TARGETARCH
COPY dist/helper-${TARGETARCH} /helper
ENTRYPOINT ["/helper"]
