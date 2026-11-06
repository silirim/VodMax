# VodMax server image. The binaries are built outside (GitHub Actions, see
# .github/workflows/docker.yml) into build/linux_<arch>/vodmax-server; this
# file only assembles the image for each platform.
# The current stable Alpine branch: ffmpeg comes from its community
# repository, which gets fixes only until the next stable branch, so move
# to each new branch (May and December) and re-test conversion with the
# graphics card. Pinned by digest: Dependabot proposes the updates.
FROM alpine:3.24@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a

# ffmpeg: pause of live TV, recordings, MKV/AVI in the browser.
# su-exec: see packaging/docker-entrypoint.sh (PUID/PGID).
# On amd64 also the Intel VAAPI driver, for conversion with the graphics
# card (passed with --device /dev/dri). AMD's (mesa) would add 165 MB.
ARG TARGETARCH
ARG TARGETVARIANT
RUN apk add --no-cache ffmpeg ca-certificates tzdata su-exec \
 && if [ "$TARGETARCH" = amd64 ]; then apk add --no-cache intel-media-driver; fi \
 && addgroup -S -g 1000 vodmax && adduser -S -u 1000 -G vodmax -h /var/lib/vodmax vodmax \
 && mkdir -p /var/lib/vodmax && chown vodmax:vodmax /var/lib/vodmax

COPY build/linux_${TARGETARCH}${TARGETVARIANT}/vodmax-server /usr/local/bin/vodmax-server
COPY build/docker-entrypoint.sh /usr/local/bin/vodmax-entrypoint

USER vodmax
VOLUME /var/lib/vodmax
EXPOSE 8095
ENV TZ=Europe/Rome
HEALTHCHECK --interval=1m --timeout=5s CMD wget -q -O /dev/null http://127.0.0.1:8095/api/v1/health || exit 1
ENTRYPOINT ["vodmax-entrypoint", "-data", "/var/lib/vodmax", "-listen", "0.0.0.0:8095"]

LABEL org.opencontainers.image.title="VodMax" \
      org.opencontainers.image.description="Self-hosted media center for live TV playlists and your movie folders" \
      org.opencontainers.image.source="https://github.com/silirim/VodMax" \
      org.opencontainers.image.url="https://vodmax.silirim.com" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
