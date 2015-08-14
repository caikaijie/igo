package httpcontext

import (
	"golang.org/x/net/context"
	"net/http"
)

// ContextHandler introduces google context(https://blog.golang.org/context) into http.Handler
// with Context, value can be easily shared between middlewares
// and it is idiomatic to implement "Go Concurrency Patterns"
// 
// middlewares(implementing ContextHandler) are encouraged to provide two functions:
// 1. context value can be easily shared
//		FromContext(context.Context) AnyData
// 2. error can be easily handled(https://blog.golang.org/errors-are-values)
//		Err(context.Context) error
type ContextHandler interface {
	ServeHTTPWithContext(parent context.Context, w http.ResponseWriter, r *http.Request) context.Context
}

// MakeHandler chains ContextHandlers as http.Handler
func MakeHandler(parent context.Context, chs ...ContextHandler) http.Handler {
	if parent == nil {
		parent = context.Background()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := parent
		for _, h := range chs {
			select {
			case <-p.Done():
				return
			default:
				c := h.ServeHTTPWithContext(p, w, r)
				if c == nil {
					// return
					c = p // restore context
				}
				p = c
			}
		}
	})
}
