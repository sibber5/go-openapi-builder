# go-openapi-builder

A fluent helper to build OpenAPI v3 documents for Go web APIs.

> **Status:** Work in progress, no stable release yet.

## Installation

```sh
go get github.com/sibber5/go-openapi-builder/openapireg
```

## Usage

```go
import (
	...
	"github.com/sibber5/go-openapi-builder/openapireg"
)

reg := openapireg.New(&openapi3.Info{
	Title:   "Example API",
	Version: "1.0.0",
}, openapireg.WithSchemaKeyPrefixesToRemove("Contracts"))

reg.AddEndpoint(http.MethodGet, "/users/{userId}").
	WithSummary("Get user by ID").
	WithTags("users").
	WithResponseWithContent(http.StatusOK, "The user with the specified ID.", reflect.TypeFor[UserDTO]()).
	WithResponse(http.StatusNotFound, "No user with the specified ID exists.")
```

### Serving the OpenAPI Document

```go
// example for serving with chi
doc, err := reg.BuildDoc().MarshalJSON()
r.Get("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
	if err != nil {
		http.Error(w, "Failed to generate OpenAPI doc: %s"+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(doc)
})
```

## License

This project is licensed under the BSD 3-Clause "New" or "Revised" License - see the [LICENSE](LICENSE) file for details.
