package fleacircus

import (
	"encoding/gob"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// The port to listen on my default.
	DEFAULT_PORT uint16 = 9999

	// The default heartbeat frequency in milliseconds for each node.
	DEFAULT_HEARTBEAT_MILLIS uint16 = 1000

	// The default number of nodes to ping per heartbeat.
	// Setting to 0 is "all known nodes". This is not wise
	// In very large systems.
	DEFAULT_MAX_NODES_TO_PING uint16 = 100

	// The default number of nodes of data to transmit in a ping.
	// Setting to 0 is "all known nodes". This is not wise
	// In very large systems.
	DEFAULT_MAX_NODES_TO_TRANSMIT uint16 = 1000

	// Millils from last update before a node is marked stale
	DEFAUT_FLAG_STALE_MILLIS uint16 = 2000

	// Millils from last update before a node is marked stale
	DEFAULT_FLAG_DEAD_MILLIS uint16 = 5000
)

func GetNowInMillis() uint32 {
	return uint32(time.Now().UnixNano() / int64(time.Millisecond))
}

type Manager struct {
	nodes map[string]*Node

	port uint16

	// Time between heartbeats
	heartbeat_millis uint16

	// Millils from last update before a node is marked stale
	flag_stale_millis uint16

	// Millils from last update before a node is marked stale
	flag_dead_millis uint16
}

func (this *Manager) GetFlagDeadMillis() uint16 {
	if this.flag_dead_millis == 0 {
		this.flag_dead_millis = DEFAULT_FLAG_DEAD_MILLIS
	}

	return this.flag_dead_millis
}

func (this *Manager) SetFlagDeadMillis(val int) {
	if val == 0 {
		this.flag_dead_millis = DEFAULT_PORT
	} else {
		this.flag_dead_millis = uint16(val)
	}
}

func (this *Manager) GetPort() uint16 {
	if this.port == 0 {
		this.port = DEFAULT_PORT
	}

	return this.port
}

func (this *Manager) SetPort(newPort int) {
	if newPort == 0 {
		this.port = DEFAULT_PORT
	} else {
		this.port = uint16(newPort)
	}
}

/**
 * Explicitly adds a node to this server's internal nodes list.
 */
func (this *Manager) AddNode(name string) {
	host, port, err := parseNodeAddress(name)

	if err != nil {
		fmt.Println("Failure to parse node name:", err)
		return
	}

	node := Node{Host: host, Port: port, Timestamp: GetNowInMillis()}

	this.registerNewNode(&node)
}

func (this *Manager) Begin() {
	go this.Listen(this.GetPort())

	for {
		time.Sleep(time.Millisecond * 1000)
		this.PruneDeadFromList()
		this.PingAllNodes()
	}
}

/**
 * Loops through the nodes map and removed the dead ones.
 */
func (this *Manager) PruneDeadFromList() {
	for k, n := range this.nodes {
		node := *n

		if n.Age() > uint32(this.GetFlagDeadMillis()) {
			fmt.Println("Node removed [dead]:", node)
			delete(this.nodes, k)
		}
	}
}

/**
 * Starts the server on the indicated node. This is a blocking operation,
 * so you probably want to execute this as a gofunc.
 */
func (this *Manager) Listen(port uint16) {
	// Listens on port
	ln, err := net.Listen("tcp", ":"+strconv.FormatInt(int64(port), 10))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Listening on port", port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}

		fmt.Println("Accepted:", conn)

		// Handle the connection
		go this.handleManagerPing(&conn)
	}
}

func (this *Manager) PingAllNodes() {
	fmt.Println(len(this.nodes), "nodes")

	for _, node := range this.nodes {
		go this.PingNode(node)
	}
}

/**
 * Initiates a ping of `count` nodes. Passing 0 is equivalent to calling
 * PingAllNodes().
 */
func (this *Manager) PingNNodes(count int) {
	rnodes := this.getRandomNodesSlice(count)

	// Loop over nodes and ping them
	for _, node := range rnodes {
		go this.PingNode(&node)
	}
}

/**
 * User-friendly method to explicitly ping a node. Calls the low-level
 * doPingNode(), and outputs a mesaage if it fails.
 */
func (this *Manager) PingNode(node *Node) error {
	err := this.doPingNode(node)
	if err != nil {
		fmt.Println("Failure to ping", node, "->", err)
	}

	return err
}

func (this *Manager) doPingNode(node *Node) error {
	conn, err := net.Dial("tcp", node.Address())
	if err != nil {
		return err
	}

	encoder := gob.NewEncoder(conn)

	err = encoder.Encode("PING")
	if err != nil {
		return err
	}

	msgNodes := this.getRandomNodesSlice(0)

	// Send the length
	//
	err = encoder.Encode(len(msgNodes))
	if err != nil {
		return err
	}

	for _, n := range msgNodes {
		err = encoder.Encode(n.Host)
		if err != nil {
			return err
		}

		err = encoder.Encode(n.Port)
		if err != nil {
			return err
		}
	}

	// Receive the response
	var response string
	err = gob.NewDecoder(conn).Decode(&response)
	if err != nil {
		fmt.Println("Error receiving response:", err)
		return err
	} else if response == "ACK" {
		node.Touch()
	}

	return nil
}

/**
 * Returns a slice of Node[] of from 0 to len(this.nodes) nodes.
 * If size is < len(this.nodes), that many nodes are randomly chosen and
 * returned.
 */
func (this *Manager) getRandomNodesSlice(size int) []Node {
	// Copy the complete nodes map into a slice
	rnodes := make([]Node, 0, len(this.nodes))
	i := 0
	for _, n := range this.nodes {
		// If a node is stale, we skip it.
		if n.Age() < uint32(this.flag_stale_millis) {
			go this.PingNode(n)
		}

		rnodes = append(rnodes, *n)
		i++
	}

	// If size is less than the entire set of nodes, shuffle and get a subset.
	if size <= 0 || size > len(this.nodes) {
		size = len(this.nodes)
	}

	if size < len(this.nodes) {
		// Shuffle the slice
		for i := range rnodes {
			j := rand.Intn(i + 1)
			rnodes[i], rnodes[j] = rnodes[j], rnodes[i]
		}

		rnodes = rnodes[0:size]
	}

	return rnodes
}

func (this *Manager) handleManagerPing(c *net.Conn) {
	var msgNodes []Node
	var verb string
	var err error

	// Every ping comes in two parts: the verb and the node list.
	// For now, the only supported very is PING; later we'll support FORWARD
	// and NFPING ("non-forwarding ping") for a full SWIM implementation.

	decoder := gob.NewDecoder(*c)

	// First, receive the verb
	//
	err = decoder.Decode(&verb)
	if err != nil {
		fmt.Println("Error receiving verb:", err)
		return
	} else {
		fmt.Println("Received verb: ", verb)
	}

	// Second, receive the list
	//
	var length int
	var host string
	var port uint16

	err = decoder.Decode(&length)
	if err != nil {
		fmt.Println("Error receiving list:", err)
		return
	}

	for i := 0; i < length; i++ {
		err = decoder.Decode(&host)
		if err != nil {
			fmt.Println("Error receiving list (host):", err)
			return
		}

		err = decoder.Decode(&port)
		if err != nil {
			fmt.Println("Error receiving list (port):", err)
			return
		}

		newNode := Node{Host: host, Port: port, Timestamp: GetNowInMillis()}
		msgNodes = append(msgNodes, newNode)
	}

	// Handle the verb
	//
	switch {
	case verb == "PING":
		err = gob.NewEncoder(*c).Encode("ACK")
	}

	// Finally, merge the list we got with received from the ping with our own list.
	for _, node := range msgNodes {
		if existingNode, ok := this.nodes[node.Address()]; ok {
			// We have this node in our list. Touch it to update the timestamp.
			existingNode.Touch()

			fmt.Println("Node exists; is now:", existingNode)
		} else {
			// We do not have this node in our list. Add it.
			this.nodes[node.Address()] = &node
			fmt.Println("New node identified:", node)
		}
	}

	(*c).Close()
}

func parseNodeAddress(hostAndMaybePort string) (string, uint16, error) {
	var host string
	var port uint16
	var err error

	if strings.Contains(hostAndMaybePort, ":") {
		splode := strings.Split(hostAndMaybePort, ":")

		if len(splode) == 2 {
			p, e := strconv.ParseUint(splode[1], 10, 16)

			host = splode[0]
			port = uint16(p)
			err = e
		} else {
			err = errors.New("too many colons in argument " + hostAndMaybePort)
		}
	} else {
		host = hostAndMaybePort
		port = DEFAULT_PORT
	}

	return host, port, err
}

func (this *Manager) registerNewNode(node *Node) {
	if this.nodes == nil {
		this.nodes = make(map[string]*Node)
	}

	fmt.Println("Adding host:", node.Address())

	this.nodes[node.Address()] = node

	fmt.Println("NOW:", len(this.nodes))
}
