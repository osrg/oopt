oopt: pkg/model/packet-transport.go
	cd cmd/oopt && CGO_ENABLE=0 go build

pkg/model/packet-transport.go: ./yang/packet-transport.yang
	generator -compress_paths -generate_fakeroot -package_name model -exclude_modules openconfig-platform,openconfig-terminal-device,openconfig-interfaces,ietf-interfaces -path ./submodules/public/release,./submodules/pyang/modules/ietf,./submodules/pyang/modules/iana  -output_file ./pkg/model/packet-transport.go ./yang/packet-transport.yang
	gofmt -w ./pkg/model/packet-transport.go

docker:
	docker build -t oopt .
	docker run --user `id -u`:`id -g` -v `pwd`:/go/src/github.com/osrg/oopt/ -w /go/src/github.com/osrg/oopt --rm oopt make

clean:
	-rm -f ./cmd/oopt/oopt ./pkg/model/packet-transport.go
