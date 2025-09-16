package oasregistry

import (
	"fmt"
	"net/http"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
)

// Registry collects operations and component schemas for endpoints to produce an OpenAPI document.
type Registry struct {
	operationIDs          map[string]struct{}
	registeredObjectTypes map[string]reflect.Type

	schemaKeyPrefixesToTrim []string

	t *openapi3.T

	built bool
}

// RegistryOption configures a Registry at creation time.
type RegistryOption func(*Registry)

// WithSchemaKeyPrefixesToTrim sets key prefixes to trim from generated schema keys.
// Useful for simplifying names by removing package prefixes for example,
// e.g. trimming "contracts." if your contracts live in that package.
func WithSchemaKeyPrefixesToTrim(prefixes ...string) RegistryOption {
	return func(r *Registry) {
		r.schemaKeyPrefixesToTrim = append(r.schemaKeyPrefixesToTrim, prefixes...)
	}
}

// New creates a new Registry configured with opts.
func New(info *openapi3.Info, opts ...RegistryOption) *Registry {
	r := &Registry{
		operationIDs:          make(map[string]struct{}),
		registeredObjectTypes: make(map[string]reflect.Type),
		t: &openapi3.T{
			OpenAPI: "3.0.3", // the latest version that kin-openapi supports at the time of writing.
			Info:    info,
			Paths:   openapi3.NewPaths(),
		},
		built: false,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// BuildSpec builds the registry and returns the (always non nil) generated OpenAPI spec.
// You can marshal the returned spec to JSON or YAML with .MarshalJSON() or .MarshalYAML() and serve it.
//
// After BuildSpec is called the Registry is considered consumed and should not be used anymore, otherwise it will panic.
func (r *Registry) BuildSpec() *openapi3.T {
	if r == nil {
		panic("nil registry")
	}
	if r.built {
		panic("registry is already built")
	}
	if r.t == nil {
		panic("registry has no OpenAPI document")
	}

	t := r.t

	r.built = true
	r.operationIDs = nil
	r.registeredObjectTypes = nil
	r.schemaKeyPrefixesToTrim = nil
	r.t = nil

	return t
}

// OperationBuilder is a fluent helper used to set operation fields.
type OperationBuilder struct {
	op *openapi3.Operation
	r  *Registry
}

// AddEndpoint registers a new operation and returns an OperationBuilder.
// Panics if operation ID is duplicated or method/path format is unsupported.
func (r *Registry) AddEndpoint(method string, path string) OperationBuilder {
	if r == nil {
		panic("nil registry")
	}
	if r.built {
		panic("cannot mutate already built registry")
	}
	id := generateID(method, path)
	if _, exists := r.operationIDs[id]; exists {
		panic(fmt.Sprintf("operation with id %s already exists", id))
	}
	r.operationIDs[id] = struct{}{}

	op := openapi3.NewOperation()
	r.register(method, path, op)
	op.OperationID = id
	return OperationBuilder{op, r}
}

func (o OperationBuilder) WithSummary(summary string) OperationBuilder {
	if o.r.built {
		panic("cannot mutate already built registry")
	}
	if o.op.Summary != "" {
		panic("summary has already been set")
	}
	o.op.Summary = summary
	return o
}

func (o OperationBuilder) WithTags(tags ...string) OperationBuilder {
	if o.r.built {
		panic("cannot mutate already built registry")
	}
	o.op.Tags = append(o.op.Tags, tags...)
	return o
}

func (o OperationBuilder) WithDescription(description string) OperationBuilder {
	if o.r.built {
		panic("cannot mutate already built registry")
	}
	if o.op.Description != "" {
		panic("description has already been set")
	}
	o.op.Description = description
	return o
}

func (o OperationBuilder) WithRequestBody(requestBodyType reflect.Type) OperationBuilder {
	if o.r.built {
		panic("cannot mutate already built registry")
	}
	if o.op.RequestBody != nil {
		panic("request body has already been set")
	}

	req := openapi3.NewRequestBody().
		WithRequired(true).
		WithJSONSchemaRef(o.r.addSchemaAndGetRef(requestBodyType))

	o.op.RequestBody = &openapi3.RequestBodyRef{Value: req}
	return o
}

func (o OperationBuilder) WithResponse(status int, description string) OperationBuilder {
	if o.r.built {
		panic("cannot mutate already built registry")
	}

	statusStr := strconv.Itoa(status)

	if status < 0 || status >= 600 {
		panic("invalid http status code: " + statusStr)
	}

	if o.op.Responses.Value(statusStr) != nil {
		panic(fmt.Sprintf("response with status code %s has already been set", statusStr))
	}

	if description == "" {
		description = http.StatusText(status)
	}

	response := openapi3.NewResponse().
		WithDescription(description)

	addResponse(o.op, statusStr, response)
	return o
}

func (o OperationBuilder) WithResponseWithContent(status int, description string, contentType reflect.Type) OperationBuilder {
	if o.r.built {
		panic("cannot mutate already built registry")
	}

	statusStr := strconv.Itoa(status)

	if status < 0 || status >= 600 {
		panic("invalid http status code: " + statusStr)
	}

	if o.op.Responses.Value(statusStr) != nil {
		panic(fmt.Sprintf("response with status code %s has already been set", statusStr))
	}

	if description == "" {
		description = http.StatusText(status)
	}

	response := openapi3.NewResponse().
		WithDescription(description).
		WithJSONSchemaRef(o.r.addSchemaAndGetRef(contentType))

	addResponse(o.op, statusStr, response)
	return o
}

func addResponse(operation *openapi3.Operation, status string, response *openapi3.Response) {
	if operation.Responses == nil {
		operation.Responses = openapi3.NewResponses()
		operation.Responses.Delete("default")
	}
	operation.Responses.Set(status, &openapi3.ResponseRef{Value: response})
}

func (r *Registry) register(method, path string, op *openapi3.Operation) {
	item := r.t.Paths.Value(path)
	if item == nil {
		item = &openapi3.PathItem{}
	}

	var opRef **openapi3.Operation
	switch method {
	case http.MethodGet:
		opRef = &item.Get
	case http.MethodPost:
		opRef = &item.Post
	case http.MethodPut:
		opRef = &item.Put
	case http.MethodDelete:
		opRef = &item.Delete
	case http.MethodPatch:
		opRef = &item.Patch
	default:
		panic("unsupported method: " + method)
	}

	if *opRef == nil {
		panic("endpoint is already registered for " + method + " " + path)
	}
	*opRef = op

	r.t.Paths.Set(path, item)
}

// this probably wont make it to stable, its just the impl im currently using that i also didnt test much
// and so is likely to be bugged somehow.
// TODO: provide a way to pass a custom generateID method in the registry constructor.
func generateID(method string, path string) string {
	getNextWord := func(p string) (string, int) {
		if p == "" {
			panic("path is empty")
		} else if p[0] == '{' || p[0] == '/' {
			panic("unsupported path format: " + p)
		}

		curWordEnd := strings.IndexByte(p, '/') // '/' is 1 byte in utf8
		if curWordEnd == -1 {
			return p, len(p)
		} else if curWordEnd == len(p)-1 {
			panic("unsupported path format, path ends with '/': " + p)
		}
		curWord := p[:curWordEnd]

		nextWordStart := curWordEnd + 1
		nextWordEnd := strings.IndexByte(p[nextWordStart:], '/')
		if nextWordEnd == -1 {
			nextWordEnd = len(p)
		} else {
			nextWordEnd += nextWordStart
		}

		nextWord := p[nextWordStart:nextWordEnd]

		// if nextWord matches {*Id} (case insensitive)
		if (len(nextWord) >= 4) &&
			(nextWord[0] == '{' && nextWord[len(nextWord)-1] == '}') && // '{' and '}' are both 1 byte in utf8
			(unicode.ToLower(rune(nextWord[len(nextWord)-3])) == 'i' && unicode.ToLower(rune(nextWord[len(nextWord)-2])) == 'd') &&
			(curWord[len(curWord)-1] == 's' && len(curWord) > 1) {
			return curWord[:(len(curWord) - 1)], nextWordEnd
		}

		if nextWord[0] == '{' {
			panic("unsupported path format: " + p)
		}

		// nextWord is just a normal word/path segment (not a param)
		return curWord, curWordEnd
	}

	var sb strings.Builder
	for _, c := range method {
		sb.WriteRune(unicode.ToLower(c))
	}

	path = path[1:] // skip first '/' from path
	n := len(path)
	for i := 0; i < n; i++ {
		if i == 0 || path[i-1] == '/' {
			word, nextIdx := getNextWord(path[i:])

			firstLetter, width := utf8.DecodeRuneInString(word)
			if firstLetter == utf8.RuneError {
				panic("unexpected first letter in word: " + word)
			}

			sb.WriteRune(unicode.ToUpper(firstLetter))
			sb.WriteString(word[width:])

			i += nextIdx
		}
	}

	return sb.String()
}

var (
	gen     = openapi3gen.NewGenerator(openapi3gen.ThrowErrorOnCycle())
	modPath = getModPath()
)

func getModPath() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		// panic("could not read build info")
		return ""
	}
	return info.Main.Path
}

// generates schema key from object type package path.
func (r *Registry) addSchemaAndGetRef(objectType reflect.Type) *openapi3.SchemaRef {
	kind := objectType.Kind()
	if kind == reflect.Map {
		panic("not implemented yet")
	} else if kind == reflect.Array || kind == reflect.Slice {
		ref := r.addSchemaAndGetRef(objectType.Elem())
		schema := openapi3.NewArraySchema()
		schema.Items = ref
		return &openapi3.SchemaRef{Value: schema}
	} else if kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface || kind == reflect.Pointer || kind == reflect.UnsafePointer || kind == reflect.Invalid {
		panic("object kind is not supported")
	} else if kind != reflect.Struct { // primitive
		s, err := gen.NewSchemaRefForValue(reflect.New(objectType).Elem().Interface(), nil)
		if err != nil {
			panic("error generating object schema: " + err.Error())
		}
		return s
	}

	// TODO: this is also problematic like generateID and wouldnt be shipped in stable.
	getObjectTypeName := func(t reflect.Type) string {
		pkgPath := t.PkgPath()
		if pkgPath == "" {
			panic("unexpected empty type package path")
		}

		if modPath != "" {
			modOrgEnd := strings.LastIndexByte(modPath, '/') + 1
			modOrg := modPath[:modOrgEnd]
			if modOrgEnd == 0 {
				panic("type package path format not supported")
			} else {
				cut, ok := strings.CutPrefix(pkgPath, modOrg)
				if !ok {
					panic("type pacakge " + pkgPath + " does not belong to same org. you must own the endpoint contracts")
				}
				pkgPath, _ = strings.CutPrefix(cut, modPath[modOrgEnd:]) // cuts entire module if t is in the same module
			}
		}

		// convert package path components to PascalCase and remove dashes.
		var sb strings.Builder
		var prev rune
		for i, c := range pkgPath {
			if c != '/' && c != '-' {
				if i == 0 || prev == '/' || prev == '-' {
					sb.WriteRune(unicode.ToUpper(c))
				} else {
					sb.WriteRune(c)
				}
			}
			prev = c
		}
		sb.WriteString(t.Name())
		res := sb.String()

		for _, p := range r.schemaKeyPrefixesToTrim {
			res, _ = strings.CutPrefix(res, p)
		}

		return res
	}
	objectTypeName := getObjectTypeName(objectType)

	regType, schemaExists := r.registeredObjectTypes[objectTypeName]
	if !schemaExists {
		if r.t.Components == nil {
			r.t.Components = &openapi3.Components{Schemas: make(openapi3.Schemas)}
		}
		schema, err := gen.NewSchemaRefForValue(reflect.New(objectType).Elem().Interface(), nil)
		if err != nil {
			panic("error generating object schema: " + err.Error())
		}

		r.t.Components.Schemas[objectTypeName] = schema
		r.registeredObjectTypes[objectTypeName] = objectType
	} else if regType != objectType {
		panic(fmt.Sprintf("object type name %v is already registered with type %v", objectTypeName, regType))
	}

	return &openapi3.SchemaRef{Ref: "#/components/schemas/" + objectTypeName}
}
