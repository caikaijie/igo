package connpool

import (
	"container/list"
	"errors"
	//"net"
	"io"
	"sync"
)

type Conn interface {
	io.Closer
	GetRaw() io.Closer
}

// conn implements Conn and hooks Conn's Close method
type conn struct {
	io.Closer
	closed bool
	cp     *node
}

func (c *conn) Close() error {
	if !c.closed {
		c.closed = true
		c.cp.closeConn(c)
	}

	// is Close idempotent?
	return c.Closer.Close()
}

func (c *conn) GetRaw() io.Closer {
	if c.closed {
		return nil
	}
	return c.Closer
}

// io.Closer?
type ConnPool interface {
	Conn() (Conn, error)
	// Put connection back to pool
	// both healthy and closed connection can be put
	// if Conn is closed, Put just drops it
	// must be Conn got from ConnPool.Conn(), or will panic
	Put(Conn)
	Close() error

	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
}

// type ConnPoolMgr interface {
// 	SetMaxOpenConns(int)
// 	SetMaxIdleConns(int)
// }

type ConnFactory interface {
	New() (io.Closer, error)
}

var (
	connectionRequestQueueSize = 1000000
	defaultMaxIdle             = 2

	errNodeClosed = errors.New("connpool node closed") // TODO: wording
)

type connRequest chan<- interface{}

type node struct {
	sync.Mutex
	maxOpen      int // <= 0 means no limit; default: 0
	maxIdle      int // < 0: means no limit; = 0 means default; default: defaultMaxIdle
	numOpen      int
	freeConn     *list.List
	connRequests *list.List
	pendingOpens int
	closed       bool

	openerCh chan struct{}
	factory  ConnFactory
}

func (n *node) Conn() (Conn, error) {
	n.Lock()
	if n.maxOpen > 0 && n.numOpen >= n.maxOpen && n.freeConn.Len() == 0 {
		ch := make(chan interface{}, 1)
		req := connRequest(ch)
		n.connRequests.PushBack(req)
		n.maybeOpenNewConnections()
		n.Unlock()
		ret, ok := <-ch
		if !ok {
			return nil, errNodeClosed
		}
		switch ret.(type) {
		case *conn:
			return ret.(*conn), nil
		case error:
			return nil, ret.(error)
		default:
			panic("connpool: Unexpected type passed through connRequest.ch")
		}
	}

	if f := n.freeConn.Front(); f != nil {
		c := f.Value.(*conn)
		n.freeConn.Remove(f)
		n.Unlock()
		return c, nil
	}

	n.numOpen++ // optimistically
	n.Unlock()

	nc, err := n.factory.New()
	if err != nil {
		n.Lock()
		n.numOpen-- // correct for earlier optimism
		n.Unlock()
		return nil, err
	}

	c := &conn{
		Closer: nc,
		cp:     n,
	}
	return c, nil
}

func (n *node) Put(nc Conn) {
	c := nc.(*conn) // panic info?
	if c.closed {
		return
	}
	n.Lock()
	n.putConnLocked(c, nil)
	n.Unlock()
}

func (n *node) Close() error {
	n.Lock()
	if n.closed {
		n.Unlock()
		return nil // duplicate close?
	}
	n.closed = true
	close(n.openerCh)
	n.Unlock()

	return nil
}

func (n *node) SetMaxOpenConns(num int) {
	n.Lock()
	n.maxOpen = num
	if num < 0 {
		n.maxOpen = 0
	}
	syncMaxIdle := n.maxOpen > 0 && n.maxIdleConnsLocked() > n.maxOpen
	n.Unlock()
	if syncMaxIdle {
		n.SetMaxIdleConns(num)
	}
}

func (n *node) SetMaxIdleConns(num int) {
	n.Lock()
	if num > 0 {
		n.maxIdle = num
	} else {
		// No idle connections.
		n.maxIdle = -1
	}
	// Make sure maxIdle doesn't exceed maxOpen
	if n.maxOpen > 0 && n.maxIdleConnsLocked() > n.maxOpen {
		n.maxIdle = n.maxOpen
	}
	var closing []*conn
	for n.freeConn.Len() > n.maxIdleConnsLocked() {
		c := n.freeConn.Back().Value.(*conn)
		n.freeConn.Remove(n.freeConn.Back())
		closing = append(closing, c)
	}
	n.Unlock()
	for _, c := range closing {
		c.Close()
	}
}

func NewNode(factory ConnFactory) (ConnPool, error) {
	if factory == nil {
		return nil, errors.New("ConnFactory can not be nil.")
	}

	n := &node{
		factory:      factory,
		freeConn:     list.New(),
		connRequests: list.New(),
		openerCh:     make(chan struct{}, connectionRequestQueueSize),
	}

	go n.connectionOpener()

	return n, nil
}

func (n *node) maybeOpenNewConnections() {
	numRequests := n.connRequests.Len() - n.pendingOpens
	if n.maxOpen > 0 {
		numCanOpen := n.maxOpen - (n.numOpen + n.pendingOpens)
		if numRequests > numCanOpen {
			numRequests = numCanOpen
		}
	}
	for numRequests > 0 {
		n.pendingOpens++
		numRequests--
		n.openerCh <- struct{}{}
	}
}

func (n *node) connectionOpener() {
	for _ = range n.openerCh {
		n.openNewConnection()
	}
}

func (n *node) openNewConnection() {
	nc, err := n.factory.New()

	n.Lock()
	defer n.Unlock()
	if n.closed {
		if err == nil {
			nc.Close()
		}
		return
	}
	n.pendingOpens--
	if err != nil {
		n.putConnLocked(nil, err)
		return
	}
	c := &conn{
		Closer: nc,
		cp:     n,
	}
	if n.putConnLocked(c, err) {
		n.numOpen++
	} else {
		nc.Close()
	}
}

func (n *node) putConnLocked(c *conn, err error) bool {
	if n.connRequests.Len() > 0 {
		req := n.connRequests.Front().Value.(connRequest)
		n.connRequests.Remove(n.connRequests.Front())
		if err != nil {
			req <- err
		} else {
			req <- c
		}
		return true
	} else if err == nil && !n.closed && n.maxIdleConnsLocked() > n.freeConn.Len() {
		n.freeConn.PushFront(c)
		return true
	}
	return false
}

func (n *node) closeConn(c *conn) {
	n.Lock()
	n.numOpen--
	n.maybeOpenNewConnections()
	n.Unlock()
}

func (n *node) maxIdleConnsLocked() int {
	i := n.maxIdle
	switch {
	case i == 0:
		return defaultMaxIdle
	case i < 0:
		return 0
	default:
		return i
	}
}
