# The hosted image (SDD §15.1): one static binary, a non-root user, no volume. goreleaser
# builds the binary and passes it in as speccy.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/speccy /usr/local/bin/speccy
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/speccy", "serve", "--hosted"]
