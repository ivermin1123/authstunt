# Build context is assembled by goreleaser: the binary for the target
# platform is cross-compiled first and copied in, so this file is COPY-only
# and a linux/arm64 image builds on an amd64 runner without emulation.
#
# distroless/static rather than scratch or alpine: the binary is static
# (CGO_ENABLED=0) and needs nothing installed, but --seed-url over https
# needs CA roots, which scratch does not carry; alpine would add a shell
# and a package manager that only widen what has to be watched.
FROM gcr.io/distroless/static-debian12:nonroot

# goreleaser lays the context out as <platform>/<binary>; buildx resolves
# TARGETPLATFORM per platform being built.
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/authstunt /authstunt

# The data directory holds the store and the encryption key, so it must
# survive the container: run with -v <volume>:/data or everything in it,
# key included, goes away with the container. No VOLUME directive on
# purpose: it would create a stray anonymous volume on every run that
# forgot the -v, hiding the loss instead of surfacing it.
#
# WORKDIR creates /data owned by the image's nonroot user (uid 65532),
# which is what lets the server write there without running as root.
WORKDIR /data

# SMTP ingest and the HTTP API.
EXPOSE 1025 8925

ENTRYPOINT ["/authstunt"]

# In a container a loopback bind is unreachable, so both listeners bind
# wide and the API names the loopback Hosts it answers to, which is the
# declaration serve requires for a non-loopback bind. Callers that reach
# the API under another name (a CI service container's hostname) override
# CMD and add their own --api-host.
#
# serve refuses to start until `project bearer provision` has run against
# the same /data volume; the quickstart does that in a `docker run -it`
# first, so the credential is only ever printed to a real terminal.
CMD ["serve", "--data-dir", "/data", \
     "--smtp-listen", "0.0.0.0:1025", \
     "--api-listen", "0.0.0.0:8925", \
     "--api-host", "localhost", "--api-host", "127.0.0.1", "--api-host", "::1"]
