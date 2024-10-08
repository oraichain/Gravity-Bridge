## Building

On first run:

```bash
sudo dnf install make automake gcc gcc-c++ kernel-devel

make

make test

make proto-update-deps

sudo make proto-tools
```

Following builds and test:

```bash
make
make test
```

To update protos after editing .proto files

```bash
make proto-gen
```

## Test

go test ./x/gravity/migrations/v2/... -v --count=1

go test ./x/gravity/migrations/v3/... -v --count=1

go test ./x/gravity/keeper/... -v --count=1

## Update swagger

```bash
go install github.com/rakyll/statik
statik -src doc/swagger-ui/ -dest doc -f
```
