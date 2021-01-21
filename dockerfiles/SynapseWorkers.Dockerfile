# This dockerfile builds on top of Dockerfile-worker and includes a built-in postgres instance
# as well as sets up the homeserver so that it is ready for testing via Complement
FROM matrixdotorg/synapse:workers

# Download a caddy server to stand in front of nginx and terminate TLS using Complement's
# custom CA.
# We include this near the top of the file in order to cache the result.
RUN curl -OL "https://github.com/caddyserver/caddy/releases/download/v2.3.0/caddy_2.3.0_linux_amd64.tar.gz" && \
  tar xzf caddy_2.3.0_linux_amd64.tar.gz && rm caddy_2.3.0_linux_amd64.tar.gz && mv caddy /root

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

# Generate a signing key
RUN generate_signing_key.py -o /conf/server.signing.key

WORKDIR /root

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
  # Note: This list currently includes all worker types other than federation_sender, as
  # Synapse fails to send federation transactions with it enabled.
  # https://github.com/matrix-org/synapse/issues/9192
  SYNAPSE_WORKERS=pusher,user_dir,media_repository,appservice,synchrotron,federation_reader,federation_inbound \
  # To use all available workers:
  #SYNAPSE_WORKERS=* \
  # Run the script that writes the necessary config files and starts supervisord, which in turn
  # starts everything else
  /configure_workers_and_start.py
