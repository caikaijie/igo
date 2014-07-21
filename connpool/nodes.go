package connpool

import (
	"bytes"
	"container/ring"
	"errors"
	"sync"
)

var errNodesClosed = errors.New("connpool nodes closed") // TODO: wording

type nodes struct {
	sync.Mutex
	closed   bool
	nodeRing *ring.Ring
}

func NewNodes(fs []ConnFactory) (ConnPool, error) {
	var ns []*node
	for _, f := range fs {
		c, err := NewNode(f)
		n := c.(*node)
		if err != nil {
			for _, n := range ns {
				n.Close()
			}
			return nil, err
		}
		ns = append(ns, n)
	}

	l := len(ns)
	if l == 0 {
		return nil, errors.New("at least one ConnFactory needed.")
	}

	nr := ring.New(l)
	for i := 0; i < nr.Len(); i++ {
		nr.Value = ns[i]
		nr = nr.Next()
	}

	cp := &nodes{
		nodeRing: nr,
	}

	return cp, nil
}

func (ns *nodes) Conn() (Conn, error) {
	ns.Lock()
	if ns.closed {
		ns.Unlock()
		return nil, errNodesClosed
	}
	// select the node
	// var best, available *node
	// nr := rc.nodeRing // for short
	// for i := 0; i < nr.Len(); i++ {
	//   n := nr.Value.(*node)
	// }

	// TODO 先第一个
	best := ns.nodeRing.Value.(*node)
	ns.Unlock()

	return best.Conn()
}

func (ns *nodes) Put(nc Conn) {
	c := nc.(*conn)
	c.cp.Put(c)
}

type closeErrs []error

func (errs closeErrs) Error() string {
	var buf bytes.Buffer
	for _, err := range errs {
		buf.WriteString(err.Error())
	}
	return buf.String()
}

// Close All
func (ns *nodes) Close() error {
	ns.Lock()
	ns.closed = true
	nr := ns.nodeRing
	var errs closeErrs
	nr.Do(func(v interface{}) {
		n := v.(*node)
		err := n.Close()
		if err != nil {
			errs = append(errs, err)
		}
	})

	if len(errs) > 0 {
		return errs
	}
	ns.Unlock()
	return nil
}

// Set All currently
func (ns *nodes) SetMaxOpenConns(int) {}

// Set All currently
func (ns *nodes) SetMaxIdleConns(int) {}
