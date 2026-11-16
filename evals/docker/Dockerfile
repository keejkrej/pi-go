FROM golang:1.27-bookworm AS builder
WORKDIR /work
COPY . /repo
RUN cd /repo \
    && CGO_ENABLED=0 go build -o /opt/evaluator/bin/pi-evals-entrypoint ./cmd/pi-evals-entrypoint \
    && mkdir -p /opt/evaluator/pkg-with-docs \
    && cp -R /repo/codingagent/utils/assets/package/. /opt/evaluator/pkg-with-docs/ \
    && cp -R /repo/codingagent/utils/assets/theme /opt/evaluator/pkg-with-docs/theme \
    && cp -R /repo/codingagent/utils/assets/export-html /opt/evaluator/pkg-with-docs/export-html \
    && cp -R /repo/codingagent/utils/assets/interactive /opt/evaluator/pkg-with-docs/assets

# Both variants use the same compiled runner and theme/export assets.
# The control removes only the coding-agent documentation whose effect the experiment measures.
FROM builder AS without-docs-install
RUN cp -R /opt/evaluator/pkg-with-docs /opt/evaluator/pkg-without-docs \
    && rm -rf \
    /opt/evaluator/pkg-without-docs/README.md \
    /opt/evaluator/pkg-without-docs/CHANGELOG.md \
    /opt/evaluator/pkg-without-docs/docs \
    /opt/evaluator/pkg-without-docs/examples

FROM debian:bookworm-slim AS evaluator
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /opt/evaluator/bin/pi-evals-entrypoint /usr/local/bin/pi-evals-entrypoint
RUN chmod 0700 /usr/local/bin/pi-evals-entrypoint \
    && mkdir -p /repo /run/pi-eval-secrets /artifacts \
    && chmod 0700 /run/pi-eval-secrets
WORKDIR /repo
ENV PI_PACKAGE_DIR=/repo/package
ENTRYPOINT ["/usr/local/bin/pi-evals-entrypoint"]

FROM evaluator AS without_docs
COPY --from=without-docs-install /opt/evaluator/pkg-without-docs /repo/package
ENV PI_EVAL_VARIANT=without_docs

FROM evaluator AS with_docs
COPY --from=builder /opt/evaluator/pkg-with-docs /repo/package
ENV PI_EVAL_VARIANT=with_docs
