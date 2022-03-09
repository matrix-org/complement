# A dockerfile which builds an image suitable for testing Synapse under
# complement.
#
# Currently this is based on the published 'synapse:latest' image -- ie, the
# most recent Synapse release.
#
# Also... none of the tests seem to pass yet. They do run though.
#
# To use it:
#
# (cd dockerfiles && docker build -t complement-synapse -f Synapse.Dockerfile .)
# COMPLEMENT_BASE_IMAGE=complement-synapse go test -v ./tests

ARG SYNAPSE_VERSION=latest

FROM matrixdotorg/synapse:${SYNAPSE_VERSION}

ENV SERVER_NAME=localhost

COPY synapse/* /conf/
COPY keys/* /ca/

# SSL key for the server (can't make the cert until we know the server name)
RUN openssl genrsa -out /conf/server.tls.key 2048

# generate a signing key
RUN /synapse/.venv/bin/generate_signing_key -o /conf/server.signing.key

WORKDIR /data

EXPOSE 8008 8448

ENTRYPOINT ["/conf/start.sh"]

HEALTHCHECK --start-period=5s --interval=1s --timeout=1s \
    CMD curl -fSs http://localhost:8008/health || exit 1
