package grpctool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bufbuild/protocompile"
	"github.com/jlian/dbx-plugin-api-tester/internal/apperr"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// proto 文件导入的限制。
//
// 请求快照会把这些文件原样存进宿主存储，而 host.storage 的单值上限是 256 KiB，
// 所以这里的上限不是"能不能编译"的问题，而是"能不能保存"的问题。
const (
	maxProtoFileCount  = 64
	maxProtoFileBytes  = 128 * 1024
	maxProtoTotalBytes = 256 * 1024
	protoCompileTime   = 15 * time.Second
	maxProtoErrorRunes = 4000
)

// ProtoFile 是用户导入的一个 .proto 文件。
//
// 编译时只认 basename：文件选择器通常拿不到目录结构，import 里的目录前缀会在
// normalizeProtoImports 里统一去掉，因此不同目录下的同名文件无法区分，必须拒绝。
type ProtoFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// protoSchema 是本地 proto 文件编译后的描述符集合。
//
// 与反射模式不同，这里不依赖服务端，因此没有 reflection 的服务也能发现和调用方法。
type protoSchema struct {
	services map[string]protoreflect.ServiceDescriptor
}

// loadProtoSchema 把导入的 .proto 源码编译成可用的 service 描述符。
func loadProtoSchema(files []ProtoFile) (*protoSchema, *apperr.Error) {
	sources, roots, pluginError := normalizeProtoFiles(files)
	if pluginError != nil {
		return nil, pluginError
	}

	ctx, cancel := context.WithTimeout(context.Background(), protoCompileTime)
	defer cancel()

	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(protoFileResolver{sources: sources}),
		// 源码位置信息只用于生成代码，这里用不到，关掉可以少占内存。
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	compiled, err := compiler.Compile(ctx, roots...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, apperr.Upstream(apperr.KindTimeout, "Compiling the imported .proto files timed out")
		}
		return nil, apperr.Proto("Cannot compile the imported .proto files: %s", protoCompileErrorText(err))
	}

	schema := &protoSchema{services: make(map[string]protoreflect.ServiceDescriptor)}
	visited := make(map[string]bool)
	for _, file := range compiled {
		collectProtoServices(file, schema.services, visited)
	}
	if len(schema.services) == 0 {
		return nil, apperr.Invalid("The imported .proto files do not declare any gRPC service")
	}
	return schema, nil
}

// collectProtoServices 沿着 import 递归收集 service。
//
// 用户可能只选中入口文件，service 定义在被 import 的文件里；只遍历编译根文件会漏掉它们。
func collectProtoServices(
	file protoreflect.FileDescriptor,
	services map[string]protoreflect.ServiceDescriptor,
	visited map[string]bool,
) {
	if file == nil || visited[file.Path()] {
		return
	}
	visited[file.Path()] = true

	declared := file.Services()
	for index := 0; index < declared.Len(); index++ {
		service := declared.Get(index)
		services[string(service.FullName())] = service
	}

	imports := file.Imports()
	for index := 0; index < imports.Len(); index++ {
		collectProtoServices(imports.Get(index).FileDescriptor, services, visited)
	}
}

// protoFileResolver 只在内存里解析用户导入的 proto。
//
// 编译前已经把 import 路径统一成 basename（见 normalizeProtoImports），
// 所以这里只需按 basename 查表；google/protobuf/* 由 standard imports 提供。
type protoFileResolver struct {
	sources map[string]string
}

func (resolver protoFileResolver) FindFileByPath(path string) (protocompile.SearchResult, error) {
	if content, ok := resolver.sources[path]; ok {
		return sourceResult(content), nil
	}
	return protocompile.SearchResult{}, protoregistry.NotFound
}

func sourceResult(content string) protocompile.SearchResult {
	return protocompile.SearchResult{Source: io.NopCloser(strings.NewReader(content))}
}

var protoImportPattern = regexp.MustCompile(`(?m)^([ \t]*import[ \t]+(?:public[ \t]+|weak[ \t]+)?")` + `([^"\r\n]+)(")`)

// normalizeProtoImports 把 import 里的目录前缀去掉。
//
// 浏览器的多选文件只给 basename，用户拿不到 "common/base.proto" 这样的路径；
// 统一成 basename 后，无论 import 写的是完整路径还是文件名都能对上。
// google/protobuf/* 是编译器的内置标准文件，不能重写。
func normalizeProtoImports(source string) string {
	return protoImportPattern.ReplaceAllStringFunc(source, func(line string) string {
		groups := protoImportPattern.FindStringSubmatch(line)
		if len(groups) != 4 {
			return line
		}
		path := groups[2]
		if !strings.Contains(path, "/") || strings.HasPrefix(path, "google/protobuf/") {
			return line
		}
		base := path[strings.LastIndex(path, "/")+1:]
		if base == "" {
			return line
		}
		return groups[1] + base + groups[3]
	})
}

// normalizeProtoFiles 校验文件名，返回按 basename 归一化的源码表和根文件列表。
//
// 编译只认 basename：同一个 basename 出现两次无法在内存里区分，直接拒绝比
// 产生两份相同符号的奇怪报错更清楚。
func normalizeProtoFiles(files []ProtoFile) (map[string]string, []string, *apperr.Error) {
	if len(files) == 0 {
		return nil, nil, apperr.Invalid("Import at least one .proto file")
	}
	if len(files) > maxProtoFileCount {
		return nil, nil, apperr.Invalid("Too many imported .proto files (limit %d)", maxProtoFileCount)
	}

	sources := make(map[string]string, len(files))
	origins := make(map[string]string, len(files))
	roots := make([]string, 0, len(files))
	totalBytes := 0
	for _, file := range files {
		name := normalizeProtoName(file.Name)
		if name == "" {
			return nil, nil, apperr.Invalid("Every imported .proto file needs a file name")
		}
		for _, segment := range strings.Split(name, "/") {
			if segment == ".." {
				return nil, nil, apperr.Invalid("Invalid .proto file name %q", file.Name)
			}
		}
		base := name
		if index := strings.LastIndex(name, "/"); index >= 0 {
			base = name[index+1:]
		}
		if base == "" {
			return nil, nil, apperr.Invalid("Invalid .proto file name %q", file.Name)
		}
		if origin, exists := origins[base]; exists {
			return nil, nil, apperr.Invalid(
				"The imported .proto files %q and %q share the file name %q; import files with unique names",
				origin, name, base)
		}
		if strings.TrimSpace(file.Content) == "" {
			return nil, nil, apperr.Invalid("The .proto file %q is empty", name)
		}
		size := len(file.Content)
		if size > maxProtoFileBytes {
			return nil, nil, apperr.Invalid(
				"The .proto file %q is larger than %d KiB", name, maxProtoFileBytes/1024)
		}
		totalBytes += size
		if totalBytes > maxProtoTotalBytes {
			return nil, nil, apperr.Invalid(
				"The imported .proto files are larger than %d KiB in total", maxProtoTotalBytes/1024)
		}
		sources[base] = normalizeProtoImports(file.Content)
		origins[base] = name
		roots = append(roots, base)
	}

	sort.Strings(roots)
	return sources, roots, nil
}

func normalizeProtoName(raw string) string {
	name := strings.TrimSpace(raw)
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	name = strings.Join(strings.Fields(name), " ")
	return name
}

func protoCompileErrorText(err error) string {
	text := strings.TrimSpace(err.Error())
	runes := []rune(text)
	if len(runes) > maxProtoErrorRunes {
		return string(runes[:maxProtoErrorRunes]) + "…"
	}
	return text
}

// serviceNames 返回按名称排序的 service，供下拉框使用。
func (schema *protoSchema) serviceNames() []string {
	names := make([]string, 0, len(schema.services))
	for name := range schema.services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (schema *protoSchema) service(name string) (protoreflect.ServiceDescriptor, error) {
	service, ok := schema.services[name]
	if !ok || service == nil {
		return nil, fmt.Errorf("service %q was not found in the imported .proto files", name)
	}
	return service, nil
}

func (schema *protoSchema) methodInfos(serviceName string) ([]MethodInfo, error) {
	service, err := schema.service(serviceName)
	if err != nil {
		return nil, err
	}
	methods := make([]MethodInfo, 0, service.Methods().Len())
	for index := 0; index < service.Methods().Len(); index++ {
		methods = append(methods, methodInfoOf(service, service.Methods().Get(index)))
	}
	return methods, nil
}

func (schema *protoSchema) findMethod(serviceName, methodName string) (protoreflect.MethodDescriptor, error) {
	service, err := schema.service(serviceName)
	if err != nil {
		return nil, err
	}
	method := service.Methods().ByName(protoreflect.Name(methodName))
	if method == nil {
		return nil, fmt.Errorf("method %q was not found on %q", methodName, serviceName)
	}
	return method, nil
}

// methodInfoOf 把描述符转成界面用的方法签名，反射与 proto 文件两条路径共用。
func methodInfoOf(service protoreflect.ServiceDescriptor, method protoreflect.MethodDescriptor) MethodInfo {
	return MethodInfo{
		Name:            string(method.Name()),
		FullName:        fmt.Sprintf("/%s/%s", service.FullName(), method.Name()),
		InputType:       string(method.Input().FullName()),
		OutputType:      string(method.Output().FullName()),
		ClientStreaming: method.IsStreamingClient(),
		ServerStreaming: method.IsStreamingServer(),
	}
}
