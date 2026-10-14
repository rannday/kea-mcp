# Public TLS test fixtures

These certificates and private keys are deliberately public integration fixtures.
Use them only with the isolated Docker lab. Do not deploy them or install this CA
into a machine's trust store.

Generated with Debian WSL OpenSSL 3.0.22 on October 14, 2026; certificates expire
in October 2050. They use ECDSA P-256. The server certificate has only the IP SAN
`127.0.0.1`, so connecting as `localhost` exercises hostname rejection.

`ca.crt` signs the server and client identities. `untrusted-ca.crt` signs a
separate client identity for rejection tests. CA signing keys are discarded after
generation. Tests copy the eight `.crt`/`.key` files into their temporary config
directory; no key generation occurs during a test run. Normal certificate
verification, including expiration, remains enabled.

## Regenerate

From the repository root in Debian WSL (or another OpenSSL-equipped POSIX shell),
run the following. It replaces only these public fixtures; commit all eight files
together and rerun TLS and mTLS integration tests in both API modes.

```sh
set -eu
fixtures=integration/testdata/certs
scratch=$(mktemp -d)
for ca in ca untrusted-ca; do
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -days 8766 -subj "/CN=PUBLIC-Kea-test-$ca" \
    -addext 'basicConstraints=critical,CA:TRUE' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' \
    -keyout "$scratch/$ca.key" -out "$fixtures/$ca.crt"
done
for name in server client untrusted-client; do
  ca=ca
  usage=clientAuth
  if [ "$name" = untrusted-client ]; then ca=untrusted-ca; fi
  if [ "$name" = server ]; then usage=serverAuth; fi
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -subj "/CN=PUBLIC-Kea-test-$name" \
    -keyout "$fixtures/$name.key" -out "$scratch/$name.csr"
  printf '%s\n' 'basicConstraints=critical,CA:FALSE' \
    'keyUsage=critical,digitalSignature' "extendedKeyUsage=$usage" > "$scratch/$name.ext"
  if [ "$name" = server ]; then
    printf '%s\n' 'subjectAltName=IP:127.0.0.1' >> "$scratch/$name.ext"
  fi
  openssl x509 -req -in "$scratch/$name.csr" \
    -CA "$fixtures/$ca.crt" -CAkey "$scratch/$ca.key" \
    -CAserial "$scratch/$ca.srl" -CAcreateserial -days 8765 \
    -extfile "$scratch/$name.ext" -out "$fixtures/$name.crt"
done
rm -f "$scratch/ca.key" "$scratch/untrusted-ca.key" \
  "$scratch/ca.srl" "$scratch/untrusted-ca.srl" \
  "$scratch/server.csr" "$scratch/client.csr" "$scratch/untrusted-client.csr" \
  "$scratch/server.ext" "$scratch/client.ext" "$scratch/untrusted-client.ext"
rmdir "$scratch"
```

Update the generation and expiration dates above after regenerating.
