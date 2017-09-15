FROM alpine:3.5

# BCHOME is where your genesis.json, key.json and other files including state are stored.
ENV BCHOME /blockfreight

# Create a blockfreight user and group first so the IDs get set the same way, even
# as the rest of this may change over time.
RUN addgroup blockfreight && \
    adduser -S -G blockfreight blockfreight

RUN mkdir -p $BCHOME && \
    chown -R blockfreight:blockfreight $BCHOME
WORKDIR $BCHOME

# Expose the blockfreight home directory as a volume since there's mutable state in there.
VOLUME $BCHOME

# jq and curl used for extracting `pub_key` from private validator while
# deploying tendermint with Kubernetes. It is nice to have bash so the users
# could execute bash commands.
RUN apk add --no-cache bash curl jq

COPY blockfreight /usr/bin/blockfreight

ENTRYPOINT ["blockfreight"]

# By default you will get the blockfreight with local MerkleEyes and in-proc Tendermint.
CMD ["start", "--dir=${BCHOME}"]
