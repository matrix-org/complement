This directory contains a key and certificate for a dummy Certificate
Authority. This is used to create certificates for the servers under test.

The files were generated with:

```
openssl genrsa -out ca.key 2048
openssl req -new -x509 -key ca.key -days 3650 -subj "/C=GB/ST=London/O=matrix.org/CN=Complement CA" -out ca.crt
```
