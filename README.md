# Onion

Onion is a tool to test the various layers of the Decentralised GW(_RHEA_) stack both in isolation and 
together vis a vis ipfs.io to flush out correctness discrepancies across the RHEA stack wrt ipfs.io. 

It does this by replaying traffic that confirms to the verifiable GW spec from a given logfile to the various layers of the RHEA stack
and also to ipfs.io and then comparing the responses from the various layers wrt ipfs.io to flag correctness discrepancies.


These layers include: 

1. Lassie
2. Saturn L1 Shim without Nginx + Lassie)
3. Nginx + Saturn L1 shim + Lassie)
4. Bifrost-GW + Saturn Nginx + L1 Shim + Lassie

# How To

1. We use docker compose to bring up all the aforementioned layers/components for Onion.
   Please clone the `add_onion_tests` branch of the https://github.com/filecoin-saturn/L1-node repo
   and follow the comprehensive instructions on https://github.com/filecoin-saturn/L1-node/tree/add_onion_tests/integration/onion for how to
   bring up the containers
   ```
   Always run `rm -rf ~/shared/onion_l1/nginx_cache` before 
   bringing up the containers to ensure a clean slate
2. Please ensure that the docker compose file you are using has the versions of the various components you want to test
   before bringing up the containers. Notes on how to update the version can also be found on the aforementioned instructions
3. Configure the `config.toml` file in the `onion` directory to point to the correct host and ports of all the layers
```
The default configuration should work fine unless you've made changes to the docker compose config
```       
4. Run `go build ./cmd/onion`
5. Run `./onion -c={COUNT_OF_UNIQUE_REQUESTS} -f={LOG_FILE_TO_REPLAY} -n_runs=1` to run one round of an Onion test.
   This will replay requests from the log file to all layers of the onion and also to ipfs.io and publish a report wrt
   response code and response bytes correctness. It will also create multiple files/artefacts in the `results` directory that you can use
   to debug correctness discrepancies

**_Note on log files:_**

The log file should be a file with new line delimited URLs, one URL for each request. The URL should be a URL that satisfies
the verifiable GW spec. Here's an example of a log file:

```
"https://{host:port}/ipfs/{cid}/metadata/2486?format=car&dag-scope=all&car-scope=all&depth=all"
"https://{host:port}/ipfs/{cid}/182?format=car&dag-scope=all&car-scope=all&depth=all"
"https://{host:port}/ipfs/{cid}/metadata/2486?format=car&dag-scope=all&car-scope=all&depth=all"
"https://{host:port}/ipfs/{cid}/182?format=car&dag-scope=all&car-scope=all&depth=all"
```
