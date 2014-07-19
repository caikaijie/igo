package httpchain

import(
  "net/http"
)

type Chain []http.Handler

type NextFunc func()

type Hook interface {
  HookProc(http.ResponseWriter, *http.Request, NextFunc)
}

func (hc Chain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  var run func(http.ResponseWriter, *http.Request, []http.Handler)

  makeNext := func(w http.ResponseWriter, r *http.Request, remaining []http.Handler) NextFunc {
    return func(){run(w, r, remaining)}
  }

  run = func(w http.ResponseWriter, r *http.Request, handlers []http.Handler){
    for i, handler := range handlers {
      if hook, ok := handler.(Hook); ok {
        var remaining []http.Handler
        if i < len(handlers) {
          remaining = handlers[i + 1:]
        }
        hook.HookProc(w, r, makeNext(w, r, remaining))
        return
      }

      handler.ServeHTTP(w, r)
    }
  }

  makeNext(w, r, []http.Handler(hc)[:])()
}

type DummyHandler struct{}

func (* DummyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {}