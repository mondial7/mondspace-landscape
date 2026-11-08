// Package infra extracts the runtime/deployment layer from infrastructure
// files: Dockerfiles, docker-compose and fly.io configuration.
package infra

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// Analyzer implements analyze.Analyzer for infrastructure files.
type Analyzer struct{}

// New returns an infrastructure analyzer.
func New() Analyzer { return Analyzer{} }

// Name implements analyze.Analyzer.
func (Analyzer) Name() string { return "infra" }

// backingImages maps well-known images to the external systems they run.
var backingImages = []struct {
	prefix string
	name   string
	kind   analyze.ExternalKind
}{
	{"postgres", "PostgreSQL", analyze.ExtDatabase}, {"postgis", "PostgreSQL", analyze.ExtDatabase},
	{"mysql", "MySQL", analyze.ExtDatabase}, {"mariadb", "MariaDB", analyze.ExtDatabase},
	{"mongo", "MongoDB", analyze.ExtDatabase}, {"redis", "Redis", analyze.ExtDatabase},
	{"valkey", "Valkey", analyze.ExtDatabase}, {"memcached", "Memcached", analyze.ExtDatabase},
	{"elasticsearch", "Elasticsearch", analyze.ExtDatabase}, {"opensearch", "OpenSearch", analyze.ExtDatabase},
	{"minio", "MinIO (S3)", analyze.ExtCloud}, {"rabbitmq", "RabbitMQ", analyze.ExtQueue},
	{"nats", "NATS", analyze.ExtQueue}, {"bitnami/kafka", "Kafka", analyze.ExtQueue},
	{"confluentinc/cp-kafka", "Kafka", analyze.ExtQueue}, {"ollama/ollama", "Ollama", analyze.ExtAI},
}

// Analyze implements analyze.Analyzer.
func (Analyzer) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	for _, f := range repo.Files {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		base := path.Base(f.Path)
		var parse func(*analyze.Facts, string, string, []byte) error
		switch {
		case f.Language == "dockerfile":
			parse = parseDockerfile
		case isCompose(base):
			parse = parseCompose
		case base == "fly.toml":
			parse = parseFly
		default:
			continue
		}
		data, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		if err := parse(&facts, repo.Name, f.Path, data); err != nil {
			facts.Warnings = append(facts.Warnings, fmt.Sprintf("infra: %s: %v", f.Path, err))
		}
	}
	return facts, nil
}

func isCompose(base string) bool {
	switch base {
	case "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
		return true
	}
	return false
}

func parseDockerfile(facts *analyze.Facts, repoName, file string, data []byte) error {
	dir := path.Dir(file)
	name := repoName
	if dir != "." {
		name = path.Base(dir)
	}
	svc := analyze.Service{Name: name, Source: file, Platform: "docker", BuildDir: dir}
	var stages []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			stages = append(stages, fields[1])
		case "EXPOSE":
			for _, p := range fields[1:] {
				svc.Ports = append(svc.Ports, strings.TrimSuffix(p, "/tcp"))
			}
		case "VOLUME":
			for _, v := range fields[1:] {
				svc.Details = append(svc.Details, "volume: "+strings.Trim(v, `[]",`))
			}
		}
	}
	if len(stages) > 0 {
		svc.Image = stages[len(stages)-1]
		var builds []string
		for _, s := range stages[:len(stages)-1] {
			builds = append(builds, "build stage: "+s)
		}
		svc.Details = append(builds, svc.Details...)
	}
	facts.Services = append(facts.Services, svc)
	return nil
}

// stringList accepts the YAML forms "x", ["a","b"] and {a: {...}, b: …}.
type stringList []string

func (s *stringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = []string{n.Value}
	case yaml.SequenceNode:
		var xs []string
		if err := n.Decode(&xs); err != nil {
			return err
		}
		*s = xs
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			*s = append(*s, n.Content[i].Value)
		}
	}
	sort.Strings(*s)
	return nil
}

// buildSpec accepts "build: ./dir" and "build: {context: ./dir}".
type buildSpec struct{ Context string }

func (b *buildSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		b.Context = n.Value
		return nil
	}
	var m struct {
		Context string `yaml:"context"`
	}
	if err := n.Decode(&m); err != nil {
		return err
	}
	b.Context = m.Context
	return nil
}

type composeFile struct {
	Services map[string]struct {
		Image     string     `yaml:"image"`
		Build     *buildSpec `yaml:"build"`
		Ports     stringList `yaml:"ports"`
		DependsOn stringList `yaml:"depends_on"`
	} `yaml:"services"`
}

func parseCompose(facts *analyze.Facts, _ string, file string, data []byte) error {
	var c composeFile
	if err := yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	names := make([]string, 0, len(c.Services))
	for n := range c.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := c.Services[name]
		if s.Build == nil {
			if ext, ok := backingStore(name, s.Image); ok {
				facts.AddExternal(ext)
				continue
			}
		}
		svc := analyze.Service{
			Name: name, Source: file, Platform: "docker-compose", Image: s.Image,
			Ports: s.Ports, DependsOn: s.DependsOn,
		}
		if s.Build != nil {
			svc.BuildDir = path.Clean(path.Join(path.Dir(file), s.Build.Context))
		}
		facts.Services = append(facts.Services, svc)
	}
	return nil
}

// backingStore recognises a compose service running a well-known image.
func backingStore(service, image string) (analyze.External, bool) {
	img := strings.SplitN(image, ":", 2)[0]
	img = strings.TrimPrefix(img, "docker.io/")
	img = strings.TrimPrefix(img, "library/")
	for _, b := range backingImages {
		if img == b.prefix || strings.HasPrefix(img, b.prefix+"/") || strings.HasSuffix(img, "/"+b.prefix) {
			return analyze.External{ID: "compose:" + service, Name: b.name, Kind: b.kind, Technology: image}, true
		}
	}
	return analyze.External{}, false
}

func parseFly(facts *analyze.Facts, repoName, file string, data []byte) error {
	svc := analyze.Service{Name: repoName, Source: file, Platform: "fly.io", BuildDir: path.Dir(file)}
	var mountSrc string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `'"`)
		switch k {
		case "app":
			svc.Name = v
		case "primary_region":
			svc.Details = append(svc.Details, "region: "+v)
		case "internal_port":
			svc.Ports = append(svc.Ports, v)
		case "source":
			mountSrc = v
		case "destination":
			svc.Details = append(svc.Details, fmt.Sprintf("volume: %s → %s", mountSrc, v))
		}
	}
	facts.Services = append(facts.Services, svc)
	facts.AddExternal(analyze.External{ID: "fly.io", Name: "Fly.io", Kind: analyze.ExtHosting})
	return nil
}
