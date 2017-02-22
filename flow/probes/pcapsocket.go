/*
 * Copyright (C) 2016 Red Hat, Inc.
 *
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 *
 */

package probes

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/skydive-project/skydive/api"
	"github.com/skydive-project/skydive/common"
	"github.com/skydive-project/skydive/config"
	"github.com/skydive-project/skydive/flow"
	"github.com/skydive-project/skydive/logging"
	"github.com/skydive-project/skydive/topology/graph"
)

type PcapSocketProbe struct {
	node      *graph.Node
	state     int64
	flowTable *flow.Table
	listener  *net.TCPListener
	port      int
}

type PcapSocketProbeHandler struct {
	graph         *graph.Graph
	addr          *net.TCPAddr
	wg            sync.WaitGroup
	probes        map[string]*PcapSocketProbe
	probesLock    sync.RWMutex
	portAllocator *common.PortAllocator
}

func (p *PcapSocketProbe) run() {
	atomic.StoreInt64(&p.state, common.RunningState)

	packetsChan := p.flowTable.Start()
	defer p.flowTable.Stop()

	for atomic.LoadInt64(&p.state) == common.RunningState {
		conn, err := p.listener.Accept()
		if err != nil {
			if atomic.LoadInt64(&p.state) == common.RunningState {
				logging.GetLogger().Errorf("Error while accepting connection: %s", err.Error())
			}
			break
		}

		pcapWriter, err := flow.NewPcapWriter(conn, packetsChan, true)
		if err != nil {
			logging.GetLogger().Errorf("Failed to create pcap writer: %s", err.Error())
			return
		}

		pcapWriter.Start()
		defer pcapWriter.Stop()
	}
}

func (p *PcapSocketProbeHandler) registerProbe(n *graph.Node, ft *flow.Table) error {
	tid, _ := n.GetFieldString("TID")
	if tid == "" {
		return fmt.Errorf("No TID for node %v", n)
	}

	if _, ok := p.probes[tid]; ok {
		return fmt.Errorf("Already registered %s", tid)
	}

	port, err := p.portAllocator.Allocate()
	if err != nil {
		return err
	}

	var tcpAddr = *p.addr
	tcpAddr.Port = port

	listener, err := net.ListenTCP("tcp", &tcpAddr)
	if err != nil {
		logging.GetLogger().Errorf("Failed to listen on TDP socket %s: %s", tcpAddr.String(), err.Error())
		return err
	}

	probe := &PcapSocketProbe{
		node:      n,
		state:     common.StoppedState,
		flowTable: ft,
		listener:  listener,
		port:      port,
	}

	p.probesLock.Lock()
	p.probes[tid] = probe
	p.probesLock.Unlock()
	p.wg.Add(1)

	p.graph.AddMetadata(n, "PCAPSocket", tcpAddr.String())

	go func() {
		defer p.wg.Done()

		probe.run()
	}()

	return nil
}

func (p *PcapSocketProbeHandler) RegisterProbe(n *graph.Node, capture *api.Capture, ft *flow.Table) error {
	return p.registerProbe(n, ft)
}

func (p *PcapSocketProbeHandler) UnregisterProbe(n *graph.Node) error {
	p.probesLock.Lock()
	defer p.probesLock.Unlock()

	tid, _ := n.GetFieldString("TID")
	if tid == "" {
		return fmt.Errorf("No TID for node %v", n)
	}

	probe, ok := p.probes[tid]
	if !ok {
		return fmt.Errorf("No registered probe for %s", tid)
	}
	delete(p.probes, tid)

	atomic.StoreInt64(&probe.state, common.StoppingState)
	probe.listener.Close()

	p.portAllocator.Release(probe.port)
	p.graph.DelMetadata(probe.node, "PCAPSocket")

	return nil
}

func (p *PcapSocketProbeHandler) Start() {
}

func (p *PcapSocketProbeHandler) Stop() {
	p.probesLock.Lock()
	defer p.probesLock.Unlock()

	for _, probe := range p.probes {
		p.UnregisterProbe(probe.node)
	}
	p.wg.Wait()
}

func NewPcapSocketProbeHandler(g *graph.Graph) (*PcapSocketProbeHandler, error) {
	listen := config.GetConfig().GetString("agent.flow.pcapsocket.bind_address")
	minPort := config.GetConfig().GetInt("agent.flow.pcapsocket.min_port")
	maxPort := config.GetConfig().GetInt("agent.flow.pcapsocket.max_port")

	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", listen, minPort))
	if err != nil {
		return nil, err
	}

	portAllocator, err := common.NewPortAllocator(minPort, maxPort)
	if err != nil {
		return nil, err
	}

	return &PcapSocketProbeHandler{
		graph:         g,
		addr:          addr,
		probes:        make(map[string]*PcapSocketProbe),
		portAllocator: portAllocator,
	}, nil
}
