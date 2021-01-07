# This dockerfile builds on top of Dockerfile-worker and includes a built-in postgres instance
# as well as sets up the homeserver so that it is ready for testing via Complement
FROM matrixdotorg/synapse:workers

# Tell Complement that we are using its custom CA
ENV COMPLEMENT_CA=true

# Install postgresql
RUN apt-get update
RUN apt-get install -y postgresql

# Configure a user and create a database for Synapse
RUN pg_ctlcluster 11 main start &&  su postgres -c "echo \
 \"ALTER USER postgres PASSWORD 'somesecret'; \
 CREATE DATABASE synapse \
  ENCODING 'UTF8' \
  LC_COLLATE='C' \
  LC_CTYPE='C' \
  template=template0;\" | psql" && pg_ctlcluster 11 main stop

# Modify the shared homeserver config with postgres support, certificate setup
# and the disabling of rate-limiting
COPY synapse/workers-shared.yaml /conf/workers/shared.yaml

# Set up TLS certificates using the custom CA
COPY keys/* /ca/

# SSL key for the server (can't make the cert until we know the server name)
RUN openssl genrsa -out /conf/server.tls.key 2048

# Generate a signing key
RUN generate_signing_key.py -o /conf/server.signing.key

WORKDIR /root

# Download a caddy server to stand in front of nginx and terminate TLS using Complement's
# custom CA
RUN curl -OL "https://github.com/caddyserver/caddy/releases/download/v2.3.0/caddy_2.3.0_linux_amd64.tar.gz" && \
  tar xzf caddy_2.3.0_linux_amd64.tar.gz && rm caddy_2.3.0_linux_amd64.tar.gz

# Copy the caddy config
COPY synapse/caddy.complement.json /root/caddy.json

# Expose caddy's listener ports
EXPOSE 8008 8448

ENTRYPOINT \
  # Replace the server name in the caddy config
  sed -i "s/{{ server_name }}/${SERVER_NAME}/g" /root/caddy.json && \
  # Start postgres
  pg_ctlcluster 11 main start > /dev/null 2>&1 && \
  # Start caddy
  /root/caddy start --config /root/caddy.json > /dev/null 2>&1 && \
  # Set the server name of the homeserver
  SYNAPSE_SERVER_NAME=${SERVER_NAME} \
  # No need to report stats here
  SYNAPSE_REPORT_STATS=no \
  # Set postgres authentication details which will be placed in the homeserver config file
  POSTGRES_PASSWORD=somesecret POSTGRES_USER=postgres POSTGRES_HOST=localhost \
  # Use all available workers
  SYNAPSE_WORKERS=* \
  # The script that write the necessary config files and starts supervisord, which in turn
  # starts everything else
  /configure_workers_and_start.py
