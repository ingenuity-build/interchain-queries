# Interchain Queries Relayer

The Interchain Queries (ICQ) Relayer watches for events emitted by the ICQ module. It makes lookups against external chains, and returns query results and proofs such that the ICQ module is able to verify proofs and trigger the appropriate downstream action.

## Configuration

The ICQ Relayer configuration is controlled by a single YAML file, the default path of which is $HOME/.icq/config.

```yaml
default_chain: quicktest-1
chains:
  quicktest-1:
    key: test
    chain-id: quicktest-1
    rpc-addr: http://172.17.0.1:20401
    grpc-addr: http://172.17.0.1:20403
    account-prefix: quick
    keyring-backend: test
    gas-adjustment: 5
    gas-prices: 0.00uqck
    key-directory: /icq/.icq/keys
    debug: false
    timeout: 30s
    output-format: json
    sign-mode: direct
  quickgaia-1:
    key: test
    chain-id: quickgaia-1
    rpc-addr: http://172.17.0.1:21401
    grpc-addr: http://172.17.0.1:21403
    account-prefix: cosmos
    keyring-backend: test
    gas-adjustment: 1.3
    gas-prices: 0.00uatom
    key-directory: /icq/.icq/keys
    debug: false
    timeout: 30s
    output-format: json
    sign-mode: direct
```

## Changelog

### v0.6.1
- Fix wg.Wait() deferal bug (#4)

### v0.6.0

- Add structured logging.
- Update Quicksilver to v0.9.0

### v0.5.0

- Upgrade SDK to v0.46
- Upgrade Quicksilver to v0.8.0

