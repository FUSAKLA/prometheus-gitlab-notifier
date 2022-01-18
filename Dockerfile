FROM alpine:3.15.0
LABEL maintainer="FUSAKLA Martin Chodúr <m.chodur@seznam.cz>"


COPY --chown=nobody:nogroup prometheus-gitlab-notifier /usr/bin/
COPY --chown=nobody:nogroup Dockerfile /

EXPOSE 9629
USER 65534

ENTRYPOINT ["/usr/bin/prometheus-gitlab-notifier"]
