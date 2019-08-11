ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox:latest
LABEL maintainer="FUSAKLA Martin Chodúr <m.chodur@seznam.cz>"

COPY --chown=nobody:nogroup .build/${OS}-${ARCH}/prometheus-gitlab-notifier /bin/prometheus-gitlab-notifier
COPY --chown=nobody:nogroup conf/default_issue.tmpl /prometheus-gitlab-notifier/conf/
COPY --chown=nobody:nogroup Dockerfile /

EXPOSE 9288
RUN mkdir -p /prometheus-gitlab-notifier && chown nobody:nogroup /prometheus-gitlab-notifier
WORKDIR /prometheus-gitlab-notifier

USER 65534

ENTRYPOINT ["/bin/prometheus-gitlab-notifier"]
