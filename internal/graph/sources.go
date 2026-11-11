package graph

import (
	"sort"
	"strings"
)

func sourceKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type sourceRecords struct {
	byPath  map[string]map[string]map[string]bool
	clipped bool
}

func newSourceRecords() *sourceRecords {
	return &sourceRecords{byPath: map[string]map[string]map[string]bool{}}
}

func (s *sourceRecords) add(path, agent, basis string) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "" {
		return
	}
	if len(agent) > 128 {
		s.clipped = true
		return
	}
	if s.byPath[path] == nil {
		s.byPath[path] = map[string]map[string]bool{}
	}
	if s.byPath[path][agent] == nil {
		if len(s.byPath[path]) >= 16 {
			s.clipped = true
			return
		}
		s.byPath[path][agent] = map[string]bool{}
	}
	s.byPath[path][agent][basis] = true
}

func (s *sourceRecords) project(nodes []Node, previous []Edge, idOf map[string]string, maxEdges int) ([]Node, []Edge, bool) {
	var edges []Edge
	seen := map[string]bool{}
	agents := map[string]bool{}
	clipped := s.clipped
	for _, path := range sourceKeys(s.byPath) {
		fileID := idOf[path]
		if fileID == "" {
			continue
		}
		for _, agent := range sourceKeys(s.byPath[path]) {
			if len(edges) >= maxEdges || !agents[agent] && len(agents) >= 16 {
				clipped = true
				continue
			}
			id := "agent:" + agent
			if !agents[agent] {
				agents[agent] = true
				nodes = append(nodes, Node{ID: id, Kind: "agent", Name: agent, Agent: agent, Source: "来源记录", Extra: "记录中出现的工具名；未核验作者"})
			}
			basis := strings.Join(sourceKeys(s.byPath[path][agent]), "、")
			addEdge(&edges, seen, Edge{Type: EdgeSource, From: id, To: fileID, Reason: basis + "标记了此工具与文件的关联；属于历史来源记录，不证明当前内容作者或会话加载。", Weight: 0.8, Confidence: "来源记录"})
		}
	}
	for _, e := range previous {
		if len(edges) >= maxEdges {
			clipped = true
			break
		}
		addEdge(&edges, seen, e)
	}
	return nodes, edges, clipped
}
