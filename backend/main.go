package main

import (
	"encoding/json"
	"log"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

const (
	pluginID      = "com.jlian.apitester"
	pluginVersion = "0.0.1"
)

type apiTester struct{}

func newAPITester() *apiTester {
	return &apiTester{}
}

func (plugin *apiTester) Handle(
	_ dbxpluginsdk.RequestContext,
	method string,
	params json.RawMessage,
	_ *dbxpluginsdk.Emitter,
) (result any, pluginError *dbxpluginsdk.PluginError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("panic while handling %s: %v", method, recovered)
			result = nil
			pluginError = dbxpluginsdk.NewError(-32603, "Internal error while handling the request")
		}
	}()

	switch method {
	case "api/ping":
		return map[string]any{
			"ok":      true,
			"id":      pluginID,
			"version": pluginVersion,
			"methods": []string{"http/send", "grpc/services", "grpc/methods", "grpc/invoke"},
		}, nil
	case "http/send":
		var request httpSendParams
		if err := decodeParams(params, &request); err != nil {
			return nil, err
		}
		return sendHTTP(request)
	case "grpc/services":
		var request grpcDiscoveryParams
		if err := decodeParams(params, &request); err != nil {
			return nil, err
		}
		return discoverGRPCServices(request)
	case "grpc/methods":
		var request grpcMethodsParams
		if err := decodeParams(params, &request); err != nil {
			return nil, err
		}
		return describeGRPCMethods(request)
	case "grpc/invoke":
		var request grpcInvokeParams
		if err := decodeParams(params, &request); err != nil {
			return nil, err
		}
		return invokeGRPC(request)
	default:
		return nil, dbxpluginsdk.MethodNotFound(method)
	}
}

func decodeParams(raw json.RawMessage, target any) *dbxpluginsdk.PluginError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return dbxpluginsdk.NewError(-32602, "Invalid request parameters: "+err.Error())
	}
	return nil
}

func invalid(message string) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32602, message)
}

func clampInt(value, fallback, minimum, maximum int) int {
	if value <= 0 {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func main() {
	metadata := dbxpluginsdk.Metadata{
		ID:           pluginID,
		Version:      pluginVersion,
		Capabilities: []string{"http", "grpc"},
	}
	server := dbxpluginsdk.NewServer(metadata, newAPITester())
	if err := server.Serve(); err != nil {
		log.Fatal(err)
	}
}
