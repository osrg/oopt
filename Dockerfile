FROM golang

COPY . /go/src/github.com/osrg/oopt
RUN cd /go/src/github.com/osrg/oopt/vendor/github.com/openconfig/ygot/generator && go install
