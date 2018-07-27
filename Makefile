go:
	generator -compress_paths -generate_fakeroot -package_name model -exclude_modules openconfig-platform,openconfig-terminal-device,openconfig-interfaces,ietf-interfaces -path ./submodules/OpenROADM_MSA_Public/model/Common,./submodules/public/release,./submodules/pyang/modules/ietf,./submodules/pyang/modules/iana  -output_file ./pkg/model/packet-transport.go ./yang/packet-transport.yang
	gofmt -w ./pkg/model/packet-transport.go

proto:
	proto_generator -yext_path oopt/vendor/github.com/openconfig/ygot/proto/yext -ywrapper_path oopt/vendor/github.com/openconfig/ygot/proto/ywrapper -package_name proto -path ./submodules/public/release,./submodules/pyang/modules/ietf,./submodules/pyang/modules/iana  -output_dir . ./yang/packet-transport.yang

python: proto
	-mkdir -p python
	protoc --python_out=python -I . -I ../ `find ./proto -name '*.proto'` `find ./oopt/vendor/github.com/openconfig/ygot/proto -name '*.proto'`
	@sh -c 'for dir in `find ./python -type d`; do touch $$dir/__init__.py; done'
