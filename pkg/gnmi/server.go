package gnmi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/openconfig/gnmi/value"
	"github.com/openconfig/ygot/experimental/ygotutils"
	"github.com/openconfig/ygot/ygot"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	dpb "github.com/golang/protobuf/protoc-gen-go/descriptor"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	cpb "google.golang.org/genproto/googleapis/rpc/code"
)

var (
	supportedEncodings = []pb.Encoding{pb.Encoding_JSON, pb.Encoding_JSON_IETF}
	ModelData          = []*pb.ModelData{{
		Name:         "packet-transport",
		Organization: "Nippon Telegraph and Telephone Corporation",
		Version:      "0.7.0",
	}}
)

// Server manages a single gNMI Server implementation. Each client that connects
// via Subscribe or Get will receive a stream of updates based on the requested
// path. Set request is processed by server too.
type Server struct {
	s      *grpc.Server
	lis    net.Listener
	port   int64
	config ygot.ValidatedGoStruct
	cMu    sync.Mutex
	model  *Model
}

// NewServer returns an initialized server.
func NewServer(model *Model, config []byte, port int64, opts []grpc.ServerOption) (*Server, error) {
	rootStruct, err := model.NewConfigStruct(config)
	if err != nil {
		return nil, err
	}

	s := grpc.NewServer(opts...)
	reflection.Register(s)

	srv := &Server{
		s:      s,
		port:   port,
		config: rootStruct,
		model:  model,
	}
	if srv.port < 0 {
		srv.port = 0
	}
	srv.lis, err = net.Listen("tcp", fmt.Sprintf(":%d", srv.port))
	if err != nil {
		return nil, fmt.Errorf("failed to open listener port %d: %v", srv.port, err)
	}
	pb.RegisterGNMIServer(srv.s, srv)
	fmt.Printf("Created Server on %s\n", srv.Address())
	return srv, nil
}

// Serve will start the Server serving and block until closed.
func (srv *Server) Serve() error {
	s := srv.s
	if s == nil {
		return fmt.Errorf("Serve() failed: not initialized")
	}
	return srv.s.Serve(srv.lis)
}

// Address returns the port the Server is listening to.
func (srv *Server) Address() string {
	addr := srv.lis.Addr().String()
	return strings.Replace(addr, "[::]", "localhost", 1)
}

// Port returns the port the server is listening to.
func (srv *Server) Port() int64 {
	return srv.port
}

// getGNMIServiceVersion returns a pointer to the gNMI service version string.
// The method is non-trivial because of the way it is defined in the proto file.
func getGNMIServiceVersion() (*string, error) {
	gzB, _ := (&pb.Update{}).Descriptor()
	r, err := gzip.NewReader(bytes.NewReader(gzB))
	if err != nil {
		return nil, fmt.Errorf("error in initializing gzip reader: %v", err)
	}
	defer r.Close()
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("error in reading gzip data: %v", err)
	}
	desc := &dpb.FileDescriptorProto{}
	if err := proto.Unmarshal(b, desc); err != nil {
		return nil, fmt.Errorf("error in unmarshaling proto: %v", err)
	}
	ver, err := proto.GetExtension(desc.Options, pb.E_GnmiService)
	if err != nil {
		return nil, fmt.Errorf("error in getting version from proto extension: %v", err)
	}
	return ver.(*string), nil
}

// gnmiFullPath builds the full path from the prefix and path.
func gnmiFullPath(prefix, path *pb.Path) *pb.Path {
	fullPath := &pb.Path{Origin: path.Origin}
	if path.GetElement() != nil {
		fullPath.Element = append(prefix.GetElement(), path.GetElement()...)
	}
	if path.GetElem() != nil {
		fullPath.Elem = append(prefix.GetElem(), path.GetElem()...)
	}
	return fullPath
}

// isNIl checks if an interface is nil or its value is nil.
func isNil(i interface{}) bool {
	if i == nil {
		return true
	}
	switch kind := reflect.ValueOf(i).Kind(); kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflect.ValueOf(i).IsNil()
	default:
		return false
	}
}

// checkEncodingAndModel checks whether encoding and models are supported by the server. Return error if anything is unsupported.
func (srv *Server) checkEncodingAndModel(encoding pb.Encoding, models []*pb.ModelData) error {
	hasSupportedEncoding := false
	for _, supportedEncodings := range supportedEncodings {
		if encoding == supportedEncodings {
			hasSupportedEncoding = true
			break
		}
	}
	if !hasSupportedEncoding {
		return fmt.Errorf("unsupported encoding: %s", pb.Encoding_name[int32(encoding)])
	}
	return nil
}

// Capabilities returns supported encodings and supported models.
func (srv *Server) Capabilities(ctx context.Context, req *pb.CapabilityRequest) (*pb.CapabilityResponse, error) {
	ver, err := getGNMIServiceVersion()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error in getting gnmi service version: %v", err)
	}
	fmt.Printf("Capabilities request received.\n")
	return &pb.CapabilityResponse{
		SupportedModels:    srv.model.modelData,
		SupportedEncodings: supportedEncodings,
		GNMIVersion:        *ver,
	}, nil
}

// Get implements the Get RPC in gNMI spec.
func (srv *Server) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	var err error

	if req.GetType() != pb.GetRequest_ALL {
		return nil, status.Errorf(codes.Unimplemented, "unsupproted request type: %s", pb.GetRequest_DataType_name[int32(req.GetType())])
	}

	if err = srv.checkEncodingAndModel(req.GetEncoding(), req.GetUseModels()); err != nil {
		return nil, status.Error(codes.Unimplemented, err.Error())
	}

	prefix := req.GetPrefix()
	paths := req.GetPath()
	notifications := make([]*pb.Notification, len(paths))

	fmt.Printf("GetRequest paths: %v\n", paths)

	for i, path := range paths {
		// Get schema node for path from config struct.
		fullPath := path
		if prefix != nil {
			fullPath = gnmiFullPath(prefix, path)
		}
		if fullPath.GetElem() == nil && fullPath.GetElement() != nil {
			return nil, status.Error(codes.Unimplemented, "deprecated path element type is unsupported")
		}
		node, stat := ygotutils.GetNode(srv.model.schemaTreeRoot, srv.config, fullPath)
		if isNil(node) || stat.GetCode() != int32(cpb.Code_OK) {
			return nil, status.Errorf(codes.NotFound, "path %v not found", fullPath)
		}

		ts := time.Now().UnixNano()

		nodeStruct, ok := node.(ygot.GoStruct)
		// Return leaf node.
		if !ok {
			var val *pb.TypedValue
			switch kind := reflect.ValueOf(node).Kind(); kind {
			case reflect.Ptr, reflect.Interface:
				var err error
				val, err = value.FromScalar(reflect.ValueOf(node).Elem().Interface())
				if err != nil {
					msg := fmt.Sprintf("leaf node %v does not contain a scalar type value: %v", path, err)
					fmt.Printf(msg)
					return nil, status.Error(codes.Internal, msg)
				}
			case reflect.Int64:
				enumMap, ok := srv.model.enumData[reflect.TypeOf(node).Name()]
				if !ok {
					return nil, status.Error(codes.Internal, "not a GoStruct enumeration type")
				}
				val = &pb.TypedValue{
					Value: &pb.TypedValue_StringVal{
						StringVal: enumMap[reflect.ValueOf(node).Int()].Name,
					},
				}
			default:
				return nil, status.Errorf(codes.Internal, "unexpected kind of leaf node type: %v %v", node, kind)
			}

			update := &pb.Update{Path: path, Val: val}
			notifications[i] = &pb.Notification{
				Timestamp: ts,
				Prefix:    prefix,
				Update:    []*pb.Update{update},
			}
			continue
		}

		if req.GetUseModels() != nil {
			return nil, status.Errorf(codes.Unimplemented, "filtering Get using use_models is unsupported, got: %v", req.GetUseModels())
		}

		// Return IETF JSON by default.
		jsonEncoder := func() (map[string]interface{}, error) {
			return ygot.ConstructIETFJSON(nodeStruct, &ygot.RFC7951JSONConfig{AppendModuleName: true})
		}
		jsonType := "IETF"
		buildUpdate := func(b []byte) *pb.Update {
			return &pb.Update{Path: path, Val: &pb.TypedValue{Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: b}}}
		}

		if req.GetEncoding() == pb.Encoding_JSON {
			jsonEncoder = func() (map[string]interface{}, error) {
				return ygot.ConstructInternalJSON(nodeStruct)
			}
			jsonType = "Internal"
			buildUpdate = func(b []byte) *pb.Update {
				return &pb.Update{Path: path, Val: &pb.TypedValue{Value: &pb.TypedValue_JsonVal{JsonVal: b}}}
			}
		}

		jsonTree, err := jsonEncoder()
		if err != nil {
			msg := fmt.Sprintf("error in constructing %s JSON tree from requested node: %v", jsonType, err)
			fmt.Printf(msg)
			return nil, status.Error(codes.Internal, msg)
		}

		jsonDump, err := json.Marshal(jsonTree)
		if err != nil {
			msg := fmt.Sprintf("error in marshaling %s JSON tree to bytes: %v", jsonType, err)
			fmt.Printf(msg)
			return nil, status.Error(codes.Internal, msg)
		}

		update := buildUpdate(jsonDump)
		notifications[i] = &pb.Notification{
			Timestamp: ts,
			Prefix:    prefix,
			Update:    []*pb.Update{update},
		}
	}

	return &pb.GetResponse{Notification: notifications}, nil

}

// Set method is not implemented.
func (srv *Server) Set(context.Context, *pb.SetRequest) (*pb.SetResponse, error) {
	return nil, grpc.Errorf(codes.Unimplemented, "Set() is not implemented")
}

// Subscribe method is not implemented.
func (srv *Server) Subscribe(stream pb.GNMI_SubscribeServer) error {
	return grpc.Errorf(codes.Unimplemented, "Subscribe() is not implemented.")
}
