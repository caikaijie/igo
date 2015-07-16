package httpcontext

import (
	"golang.org/x/net/context"
	"net/http"
)

type ContextHandler interface {
	ServeHTTPWithContext(parent context.Context, w http.ResponseWriter, r *http.Request) context.Context
}

// MakeHandler
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
