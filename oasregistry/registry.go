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

type Registry struct {
	operationIds          map[string]struct{}
	registeredObjectTypes map[string]reflect.Type

	schemaKeyPrefixesToRemove []string

	t *openapi3.T
}

func NewRegistry(info *openapi3.Info, schemaKeyPrefixesToRemove ...string) *Registry {
	return &Registry{
		operationIds:              make(map[string]struct{}),
		registeredObjectTypes:     make(map[string]reflect.Type),
		schemaKeyPrefixesToRemove: schemaKeyPrefixesToRemove,
		t: &openapi3.T{
			OpenAPI: "3.0.3",
			Info:    info,
			Paths:   openapi3.NewPaths(),
		},
	}
}

func (r *Registry) BuildSpec() *openapi3.T {
	t := r.t
	(*r) = Registry{}
	return t
}

type OperationBuilder struct {
	op *openapi3.Operation
	r  *Registry
}

func (r *Registry) AddEndpoint(method string, path string) OperationBuilder {
	id := generateId(method, path)
	_, exists := r.operationIds[id]
	if exists {
		panic(fmt.Sprintf("operation with id %s already exists.", id))
	}
	r.operationIds[id] = struct{}{}

	op := openapi3.NewOperation()
	r.register(method, path, op)
	op.OperationID = id
	return OperationBuilder{op, r}
}

func (o OperationBuilder) WithSummary(summary string) OperationBuilder {
	if o.op.Summary != "" {
		panic("summary has already been set")
	}
	o.op.Summary = summary
	return o
}

func (o OperationBuilder) WithTags(tags ...string) OperationBuilder {
	o.op.Tags = append(o.op.Tags, tags...)
	return o
}

func (o OperationBuilder) WithDescription(description string) OperationBuilder {
	if o.op.Description != "" {
		panic("description has already been set")
	}
	o.op.Description = description
	return o
}

func (o OperationBuilder) WithRequestBody(requestBodyType reflect.Type) OperationBuilder {
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

	if opRef == nil {
		panic("endpoint is already registered")
	}
	(*opRef) = op

	r.t.Paths.Set(path, item)
}

func generateId(method string, path string) string {
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

var gen = openapi3gen.NewGenerator(openapi3gen.ThrowErrorOnCycle())
var modPath = getModPath()

func getModPath() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		panic("could not read build info")
	}
	return info.Main.Path
}

// generates schema key from object type package path
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

	getObjectTypeName := func(t reflect.Type) string {
		pkgPath := t.PkgPath()
		if pkgPath == "" {
			panic("unexpected empty type package path")
		}

		modUrlEnd := strings.LastIndexByte(modPath, '/') + 1
		modUrl := modPath[:modUrlEnd]
		if modUrlEnd == 0 {
			panic("type package path format not supported")
		} else {
			cut, ok := strings.CutPrefix(pkgPath, modUrl)
			if !ok {
				panic("type pacakge " + pkgPath + " does not belong to same org. you must own the endpoint contracts")
			}
			pkgPath, _ = strings.CutPrefix(cut, modPath[modUrlEnd:]) // cuts entire module if t is in the same module
		}

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

		for _, p := range r.schemaKeyPrefixesToRemove {
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
