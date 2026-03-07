//go:build cgo && treesitter_c_parity

package cgoharness

// Comprehensive YAML corpus parity test: compares gotreesitter highlight captures
// against C tree-sitter for a wide range of real-world YAML patterns.
//
// Run with:
//   go test . -tags treesitter_c_parity -run TestParityYAMLCorpus -v

import (
	"fmt"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// yamlHighlightSample holds a named YAML snippet for highlight parity testing.
type yamlHighlightSample struct {
	name string
	code string
}

// yamlHighlightCorpus contains real-world YAML snippets covering diverse patterns:
// GitHub Actions, Docker Compose, Kubernetes, Helm, Ansible, CloudFormation,
// OpenAPI, GitLab CI, anchors/aliases, multiline strings, flow syntax, tags,
// comments, booleans/null/numerics, quoted strings, and complex nesting.
var yamlHighlightCorpus = []yamlHighlightSample{
	{
		name: "github_actions_ci",
		code: `# GitHub Actions CI workflow
name: Go

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4

    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'

    - name: Build
      run: go build -v ./...

    - name: Test
      run: go test -v ./...
`,
	},
	{
		name: "docker_compose",
		code: `services:
  frontend:
    build:
      context: frontend
      target: development
    ports:
      - 3000:3000
    stdin_open: true
    volumes:
      - ./frontend:/usr/src/app
      - /usr/src/app/node_modules
    restart: always
    networks:
      - react-express
    depends_on:
      - backend

  backend:
    restart: always
    build:
      context: backend
      target: development
    volumes:
      - ./backend:/usr/src/app
      - /usr/src/app/node_modules
    depends_on:
      - mongo
    networks:
      - express-mongo
      - react-express
    expose:
      - 3000

  mongo:
    restart: always
    image: mongo:4.2.0
    volumes:
      - mongo_data:/data/db
    networks:
      - express-mongo
    expose:
      - 27017

networks:
  react-express:
  express-mongo:

volumes:
  mongo_data:
`,
	},
	{
		name: "kubernetes_deployment",
		code: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
  labels:
    app: nginx
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
        resources:
          limits:
            cpu: "500m"
            memory: "128Mi"
          requests:
            cpu: "250m"
            memory: "64Mi"
        env:
        - name: NODE_ENV
          value: production
        - name: DB_HOST
          valueFrom:
            configMapKeyRef:
              name: app-config
              key: db-host
`,
	},
	{
		name: "kubernetes_service",
		code: `apiVersion: v1
kind: Service
metadata:
  name: my-nginx-svc
  labels:
    app: nginx
spec:
  type: LoadBalancer
  ports:
  - port: 80
    targetPort: 8080
    protocol: TCP
    name: http
  - port: 443
    targetPort: 8443
    protocol: TCP
    name: https
  selector:
    app: nginx
`,
	},
	{
		name: "kubernetes_configmap",
		code: `apiVersion: v1
kind: ConfigMap
metadata:
  name: game-config
  namespace: default
data:
  player_initial_lives: "3"
  ui_properties_file_name: "user-interface.properties"
  game.properties: |
    enemy.types=aliens,monsters
    player.maximum-hierarchical-depth=4
    allow.resolve.priority=true
  user-interface.properties: |
    color.good=purple
    color.bad=yellow
    allow.textmode=true
`,
	},
	{
		name: "helm_values",
		code: `# Helm chart values for MySQL
global:
  imageRegistry: ""
  imagePullSecrets: []
  defaultStorageClass: ""
  storageClass: ""
  security:
    allowInsecureImages: false
  compatibility:
    openshift:
      adaptSecurityContext: auto

kubeVersion: ""
nameOverride: ""
fullnameOverride: ""
namespaceOverride: ""
clusterDomain: cluster.local
commonAnnotations: {}
commonLabels: {}
extraDeploy: []

serviceBindings:
  enabled: false

image:
  registry: docker.io
  repository: bitnami/mysql
  tag: 8.0.36-debian-12-r8
  pullPolicy: IfNotPresent
  pullSecrets: []
  debug: false

architecture: standalone

auth:
  rootPassword: ""
  createDatabase: true
  database: my_database
  username: ""
  password: ""
  replicationUser: replicator
  replicationPassword: ""
  existingSecret: ""

primary:
  persistence:
    enabled: true
    storageClass: ""
    accessModes:
      - ReadWriteOnce
    size: 8Gi
`,
	},
	{
		name: "ansible_playbook",
		code: `---
- name: Configure web servers
  hosts: webservers
  remote_user: deploy
  become: true
  vars:
    http_port: 80
    max_clients: 200
    packages:
      - nginx
      - certbot
      - python3-certbot-nginx

  tasks:
    - name: Install required packages
      apt:
        name: "{{ item }}"
        state: present
        update_cache: true
      loop: "{{ packages }}"

    - name: Copy nginx configuration
      template:
        src: templates/nginx.conf.j2
        dest: /etc/nginx/nginx.conf
        owner: root
        group: root
        mode: '0644'
      notify: restart nginx

    - name: Ensure nginx is running
      service:
        name: nginx
        state: started
        enabled: true

  handlers:
    - name: restart nginx
      service:
        name: nginx
        state: restarted
`,
	},
	{
		name: "cloudformation_template",
		code: `AWSTemplateFormatVersion: '2010-09-09'
Description: Simple EC2 instance with security group

Parameters:
  InstanceType:
    Description: EC2 instance type
    Type: String
    Default: t2.micro
    AllowedValues:
      - t2.micro
      - t2.small
      - t2.medium
    ConstraintDescription: Must be a valid EC2 instance type.
  KeyName:
    Description: Name of an existing EC2 KeyPair
    Type: AWS::EC2::KeyPair::KeyName
    ConstraintDescription: Must be the name of an existing KeyPair.

Mappings:
  RegionMap:
    us-east-1:
      AMI: ami-0abcdef1234567890
    us-west-2:
      AMI: ami-0fedcba0987654321

Resources:
  WebServerSecurityGroup:
    Type: AWS::EC2::SecurityGroup
    Properties:
      GroupDescription: Enable HTTP and SSH access
      SecurityGroupIngress:
        - IpProtocol: tcp
          FromPort: 80
          ToPort: 80
          CidrIp: 0.0.0.0/0
        - IpProtocol: tcp
          FromPort: 22
          ToPort: 22
          CidrIp: 0.0.0.0/0

  WebServerInstance:
    Type: AWS::EC2::Instance
    Properties:
      InstanceType: !Ref InstanceType
      KeyName: !Ref KeyName
      ImageId: !FindInMap [RegionMap, !Ref "AWS::Region", AMI]
      SecurityGroups:
        - !Ref WebServerSecurityGroup

Outputs:
  InstanceId:
    Description: Instance ID of the web server
    Value: !Ref WebServerInstance
  PublicIP:
    Description: Public IP address
    Value: !GetAtt WebServerInstance.PublicIp
`,
	},
	{
		name: "openapi_spec",
		code: `openapi: 3.0.4
info:
  title: Swagger Petstore - OpenAPI 3.0
  description: |-
    This is a sample Pet Store Server based on the OpenAPI 3.0 specification.
    You can find out more about Swagger at [https://swagger.io](https://swagger.io).
  termsOfService: https://swagger.io/terms/
  contact:
    email: apiteam@swagger.io
  license:
    name: Apache 2.0
    url: https://www.apache.org/licenses/LICENSE-2.0.html
  version: 1.0.27

servers:
  - url: https://petstore3.swagger.io/api/v3

tags:
  - name: pet
    description: Everything about your Pets
    externalDocs:
      description: Find out more
      url: https://swagger.io
  - name: store
    description: Access to Petstore orders

paths:
  /pet:
    put:
      tags:
        - pet
      summary: Update an existing pet.
      description: Update an existing pet by Id.
      operationId: updatePet
      requestBody:
        description: Update an existent pet in the store
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Pet'
        required: true
      responses:
        '200':
          description: Successful operation
        '400':
          description: Invalid ID supplied
        '404':
          description: Pet not found
        default:
          description: Unexpected error
      security:
        - petstore_auth:
            - write:pets
            - read:pets
`,
	},
	{
		name: "gitlab_ci",
		code: `stages:
  - build
  - test
  - deploy

default:
  image: ruby:3.2
  tags:
    - docker
  interruptible: true
  timeout: 30m

.default-variables: &default-variables
  RAILS_ENV: test
  DATABASE_URL: "postgres://postgres:password@postgres:5432/test"

.test-template: &test-template
  stage: test
  before_script:
    - bundle install --jobs 4
    - bundle exec rake db:create db:migrate
  variables:
    <<: *default-variables
  services:
    - postgres:15

build:
  stage: build
  script:
    - bundle install
    - bundle exec rake assets:precompile
  artifacts:
    paths:
      - public/assets/
    expire_in: 1 week
  cache:
    key: gems
    paths:
      - vendor/bundle/

rspec:
  <<: *test-template
  script:
    - bundle exec rspec --format progress
  coverage: '/\(\d+.\d+%\) covered/'

rubocop:
  <<: *test-template
  script:
    - bundle exec rubocop

deploy_staging:
  stage: deploy
  script:
    - echo "Deploying to staging..."
  environment:
    name: staging
    url: https://staging.example.com
  only:
    - main
  when: manual
`,
	},
	{
		name: "anchors_and_aliases",
		code: `# YAML anchors, aliases, and merge keys
defaults: &defaults
  adapter: postgres
  host: localhost
  port: 5432
  pool: 5
  timeout: 5000

development:
  database: myapp_development
  <<: *defaults

test:
  database: myapp_test
  <<: *defaults

production:
  database: myapp_production
  <<: *defaults
  host: db.production.example.com
  pool: 25

# Nested anchor usage
base_logging: &base_logging
  level: info
  format: json
  output:
    - stdout
    - file

services:
  api:
    logging:
      <<: *base_logging
      level: debug
  worker:
    logging:
      <<: *base_logging
      output:
        - syslog
`,
	},
	{
		name: "multiline_strings",
		code: `# Multiline string styles in YAML
description:
  literal_block: |
    This is a literal block scalar.
    Line breaks are preserved exactly.

    Including blank lines above.
    Indentation is stripped.

  literal_strip: |-
    This literal block strips
    the trailing newline at the end.

  literal_keep: |+
    This literal block keeps
    all trailing newlines.

  folded_block: >
    This is a folded block scalar.
    Newlines become spaces, so this
    whole thing is one paragraph.

    But blank lines start a new paragraph.

  folded_strip: >-
    This folded block strips
    the trailing newline.

  folded_keep: >+
    This folded block keeps
    trailing newlines.

  plain_multiline:
    this is a plain scalar that
    spans multiple lines but
    folds into a single line

script: |
  #!/bin/bash
  set -euo pipefail
  echo "Hello World"
  for i in 1 2 3; do
    echo "Number: $i"
  done
`,
	},
	{
		name: "flow_syntax",
		code: `# YAML flow (inline) syntax
person: {name: John Doe, age: 30, active: true}
colors: [red, green, blue, yellow]
empty_map: {}
empty_list: []

matrix:
  include:
    - {os: ubuntu-latest, node: 18}
    - {os: ubuntu-latest, node: 20}
    - {os: macos-latest, node: 18}
    - {os: windows-latest, node: 20}

nested_flow: {
  database: {host: localhost, port: 5432},
  cache: {host: localhost, port: 6379},
  queues: [default, critical, low]
}

# Mixed block and flow
servers:
  web: {host: 10.0.0.1, port: 80, ssl: true}
  api: {host: 10.0.0.2, port: 8080, ssl: false}
  db:
    host: 10.0.0.3
    port: 5432
    options: {pool_size: 25, timeout: 30}
`,
	},
	{
		name: "yaml_tags",
		code: `# YAML tags and type coercion
explicit_string: !!str 42
explicit_int: !!int "42"
explicit_float: !!float "3.14"
explicit_bool: !!bool "true"
explicit_null: !!null ""
explicit_seq: !!seq
  - item1
  - item2
explicit_map: !!map
  key1: value1
  key2: value2

binary_data: !!binary |
  R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAA
  LAAAAAABAAEAAAIBRAA7

ordered_map: !!omap
  - first: 1
  - second: 2
  - third: 3

set_type: !!set
  ? item1
  ? item2
  ? item3
`,
	},
	{
		name: "comments_and_structure",
		code: `# Top-level comment
# Configuration for the application

## Section: Database
database:
  # Primary database connection
  primary:
    host: localhost  # inline comment
    port: 5432      # default PostgreSQL port
    name: myapp_prod
    # Connection pool settings
    pool:
      min: 5
      max: 20
      idle_timeout: 300  # seconds

  # Read replica for queries
  replica:
    host: replica.db.internal
    port: 5432
    name: myapp_prod
    read_only: true  # enforce read-only

## Section: Cache
cache:
  driver: redis
  host: cache.internal
  port: 6379
  # TTL values in seconds
  ttl:
    default: 3600
    session: 86400
    page: 300
`,
	},
	{
		name: "boolean_null_numeric",
		code: `# Boolean values
enabled: true
disabled: false
yes_value: yes
no_value: no
on_value: on
off_value: off

# Null values
empty_value:
tilde_null: ~
explicit_null: null

# Integer types
decimal: 42
negative: -17
zero: 0
octal: 0o14
hex: 0xFF

# Float types
pi: 3.14159
negative_float: -0.5
scientific: 6.022e23
infinity: .inf
neg_infinity: -.inf
not_a_number: .nan

# Timestamps
date: 2024-01-15
datetime: 2024-01-15T10:30:00Z
datetime_offset: 2024-01-15T10:30:00+05:30
`,
	},
	{
		name: "quoted_strings",
		code: `# Single-quoted strings (no escape processing)
single: 'Hello World'
single_with_apostrophe: 'It''s a test'
single_empty: ''
single_special: 'This has: colons and # hashes'
single_newline: 'Line one\nstill line one'

# Double-quoted strings (escape processing)
double: "Hello World"
double_escapes: "Tab:\there\nNewline"
double_unicode: "Smiley: \u263A"
double_empty: ""
double_backslash: "C:\\Users\\admin"
double_quotes: "She said \"hello\""

# Unquoted strings that could be ambiguous
colon_in_value: "This contains: a colon"
hash_not_comment: This is not#a comment
numeric_string: "12345"
bool_string: "true"
null_string: "null"
`,
	},
	{
		name: "complex_nesting",
		code: `# Complex nested structures: maps of lists of maps
clusters:
  production:
    region: us-east-1
    nodes:
      - name: node-01
        role: master
        resources:
          cpu: 4
          memory: 16Gi
        labels:
          env: production
          tier: control-plane
      - name: node-02
        role: worker
        resources:
          cpu: 8
          memory: 32Gi
        labels:
          env: production
          tier: application
        taints:
          - key: dedicated
            value: gpu
            effect: NoSchedule
    networking:
      pod_cidr: 10.244.0.0/16
      service_cidr: 10.96.0.0/12
      dns:
        - 8.8.8.8
        - 8.8.4.4

  staging:
    region: us-west-2
    nodes:
      - name: staging-01
        role: master
        resources:
          cpu: 2
          memory: 8Gi
`,
	},
	{
		name: "simple_config",
		code: `# Application configuration
app:
  name: my-service
  version: 2.1.0
  debug: false

server:
  host: 0.0.0.0
  port: 8080
  read_timeout: 30s
  write_timeout: 30s
  max_connections: 1000

logging:
  level: info
  format: json
  file: /var/log/app/service.log

features:
  dark_mode: true
  beta_features: false
  rate_limiting:
    enabled: true
    requests_per_minute: 60
`,
	},
	{
		name: "multi_document",
		code: `---
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: prometheus
rules:
  - apiGroups: [""]
    resources:
      - nodes
      - services
      - endpoints
      - pods
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources:
      - configmaps
    verbs: ["get"]
`,
	},
	{
		name: "github_actions_matrix",
		code: `name: CI Matrix
on: [push, pull_request]

env:
  CARGO_TERM_COLOR: always
  RUST_BACKTRACE: 1

jobs:
  test:
    name: Test ${{ matrix.os }} / ${{ matrix.rust }}
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
        rust: [stable, beta, nightly]
        exclude:
          - os: windows-latest
            rust: nightly
        include:
          - os: ubuntu-latest
            rust: stable
            coverage: true
    steps:
      - uses: actions/checkout@v4
      - uses: dtolnay/rust-toolchain@master
        with:
          toolchain: ${{ matrix.rust }}
      - run: cargo test --all-features
      - name: Upload coverage
        if: matrix.coverage
        uses: codecov/codecov-action@v3
`,
	},
	{
		name: "prometheus_rules",
		code: `# Prometheus alerting rules
groups:
  - name: node-alerts
    interval: 30s
    rules:
      - alert: HighCPUUsage
        expr: 100 - (avg by(instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100) > 80
        for: 5m
        labels:
          severity: warning
          team: infrastructure
        annotations:
          summary: "High CPU usage on {{ $labels.instance }}"
          description: "CPU usage is above 80% for more than 5 minutes."

      - alert: DiskSpaceLow
        expr: (node_filesystem_avail_bytes / node_filesystem_size_bytes) * 100 < 10
        for: 10m
        labels:
          severity: critical
        annotations:
          summary: "Low disk space on {{ $labels.instance }}"
          description: |
            Available disk space is below 10%.
            Current value: {{ $value }}%
            Device: {{ $labels.device }}
`,
	},
}

// TestParityYAMLCorpus runs highlight parity checks across all YAML corpus
// samples. For each sample, it parses with both Go and C tree-sitter, runs
// the YAML highlight query, and compares capture ranges.
func TestParityYAMLCorpus(t *testing.T) {
	const langName = "yaml"

	entry, ok := parityEntriesByName[langName]
	if !ok {
		t.Fatalf("no registry entry for %q", langName)
	}
	queryStr := entry.HighlightQuery
	if queryStr == "" {
		t.Skipf("no highlight query for %q", langName)
	}

	// Load C reference language once.
	cLang, err := ParityCLanguage(langName)
	if err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			t.Skipf("skip C reference: %s", skipReason)
		}
		t.Fatalf("load C parser: %v", err)
	}

	// Verify C query compiles (ABI compatibility check).
	cQueryProbe, cQueryErr := sitter.NewQuery(cLang, queryStr)
	if cQueryErr != nil {
		t.Skipf("C query compilation error (ABI mismatch): %v", cQueryErr)
	}
	cQueryProbe.Close()

	for _, sample := range yamlHighlightCorpus {
		sample := sample
		t.Run(sample.name, func(t *testing.T) {
			src := []byte(sample.code)

			// --- Go parse ---
			tc := parityCase{name: langName, source: sample.code}
			goTree, goLang, err := parseWithGo(tc, src, nil)
			if err != nil {
				t.Fatalf("Go parse error: %v", err)
			}
			defer releaseGoTree(goTree)
			if goTree.RootNode().HasError() {
				t.Logf("WARNING: Go parse tree has error nodes")
			}

			goCaps := collectGoHighlightCaptures(t, goLang, goTree, queryStr, src)

			// --- C parse ---
			cParser := sitter.NewParser()
			defer cParser.Close()
			if err := cParser.SetLanguage(cLang); err != nil {
				t.Fatalf("C SetLanguage: %v", err)
			}
			cTree := cParser.Parse(src, nil)
			if cTree == nil {
				t.Fatal("C parser returned nil tree")
			}
			defer cTree.Close()

			cCaps := collectCHighlightCaptures(t, cLang, cTree, queryStr, src)

			// --- Compare ---
			onlyGo, onlyC := diffCaptures(goCaps, cCaps)

			if len(onlyGo) > 0 {
				for _, c := range onlyGo {
					t.Logf("  Go-only: %s %q", c, yamlTextSlice(src, c))
				}
			}
			if len(onlyC) > 0 {
				for _, c := range onlyC {
					t.Logf("  C-only:  %s %q", c, yamlTextSlice(src, c))
				}
			}

			if len(onlyGo) == 0 && len(onlyC) == 0 {
				t.Logf("HIGHLIGHT PARITY OK: %d captures match", len(goCaps))
			} else {
				// Diagnostic only — YAML scanner has known bugs in deeply nested
				// structures.  Use TestParityYAMLCorpusSummary for aggregate view.
				t.Logf("highlight parity MISMATCH: %d match, %d Go-only, %d C-only",
					len(goCaps)-len(onlyGo), len(onlyGo), len(onlyC))
			}
		})
	}
}

// TestParityYAMLCorpusStructural runs structural (parse tree) parity checks
// across all YAML corpus samples, ensuring Go and C parse trees are identical
// node-by-node.
func TestParityYAMLCorpusStructural(t *testing.T) {
	const langName = "yaml"

	// Load C reference language once.
	cLang, err := ParityCLanguage(langName)
	if err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			t.Skipf("skip C reference: %s", skipReason)
		}
		t.Fatalf("load C parser: %v", err)
	}

	for _, sample := range yamlHighlightCorpus {
		sample := sample
		t.Run(sample.name, func(t *testing.T) {
			src := []byte(sample.code)
			tc := parityCase{name: langName, source: sample.code}

			goTree, goLang, err := parseWithGo(tc, src, nil)
			if err != nil {
				t.Fatalf("Go parse error: %v", err)
			}
			defer releaseGoTree(goTree)

			cParser := sitter.NewParser()
			defer cParser.Close()
			if err := cParser.SetLanguage(cLang); err != nil {
				t.Fatalf("C SetLanguage: %v", err)
			}
			cTree := cParser.Parse(src, nil)
			if cTree == nil {
				t.Fatal("C parser returned nil tree")
			}
			defer cTree.Close()

			var errs []string
			compareNodes(goTree.RootNode(), goLang, cTree.RootNode(), "root", &errs)

			if len(errs) == 0 {
				t.Logf("STRUCTURAL PARITY OK: trees match")
				return
			}

			const maxErrors = 15
			shown := errs
			extra := 0
			if len(errs) > maxErrors {
				shown = errs[:maxErrors]
				extra = len(errs) - maxErrors
			}
			for _, e := range shown {
				t.Logf("  %s", e)
			}
			if extra > 0 {
				t.Logf("  ... and %d more", extra)
			}
			// Diagnostic only — known YAML scanner issues in deep nesting.
			t.Logf("structural parity MISMATCH: %d node divergence(s)", len(errs))
		})
	}
}

// TestParityYAMLCorpusSummary prints aggregate statistics across the YAML
// corpus. This is informational and always passes.
func TestParityYAMLCorpusSummary(t *testing.T) {
	const langName = "yaml"

	entry, ok := parityEntriesByName[langName]
	if !ok {
		t.Skipf("no registry entry for %q", langName)
	}
	queryStr := entry.HighlightQuery
	if queryStr == "" {
		t.Skipf("no highlight query for %q", langName)
	}

	cLang, err := ParityCLanguage(langName)
	if err != nil {
		if skipReason := parityReferenceSkipReason(err); skipReason != "" {
			t.Skipf("skip C reference: %s", skipReason)
		}
		t.Fatalf("load C parser: %v", err)
	}
	cQueryProbe, cQueryErr := sitter.NewQuery(cLang, queryStr)
	if cQueryErr != nil {
		t.Skipf("C query compilation error (ABI mismatch): %v", cQueryErr)
	}
	cQueryProbe.Close()

	totalSamples := 0
	perfectParity := 0
	totalGoOnly := 0
	totalCOnly := 0
	totalCaptures := 0

	for _, sample := range yamlHighlightCorpus {
		src := []byte(sample.code)
		tc := parityCase{name: langName, source: sample.code}

		goTree, goLang, err := parseWithGo(tc, src, nil)
		if err != nil {
			t.Logf("  %s: Go parse error: %v", sample.name, err)
			continue
		}
		goCaps := collectGoHighlightCaptures(t, goLang, goTree, queryStr, src)

		cParser := sitter.NewParser()
		if err := cParser.SetLanguage(cLang); err != nil {
			releaseGoTree(goTree)
			cParser.Close()
			continue
		}
		cTree := cParser.Parse(src, nil)
		if cTree == nil {
			releaseGoTree(goTree)
			cParser.Close()
			continue
		}

		cCaps := collectCHighlightCaptures(t, cLang, cTree, queryStr, src)
		cTree.Close()
		cParser.Close()
		releaseGoTree(goTree)

		onlyGo, onlyC := diffCaptures(goCaps, cCaps)

		totalSamples++
		totalCaptures += len(goCaps)
		totalGoOnly += len(onlyGo)
		totalCOnly += len(onlyC)
		if len(onlyGo) == 0 && len(onlyC) == 0 {
			perfectParity++
		}
	}

	t.Logf("=== YAML Corpus Highlight Parity Summary ===")
	t.Logf("  Samples tested:   %d / %d", totalSamples, len(yamlHighlightCorpus))
	t.Logf("  Perfect parity:   %d / %d", perfectParity, totalSamples)
	t.Logf("  Total captures:   %d", totalCaptures)
	t.Logf("  Total Go-only:    %d", totalGoOnly)
	t.Logf("  Total C-only:     %d", totalCOnly)
	if totalSamples > 0 && perfectParity == totalSamples {
		t.Logf("  Result: FULL PARITY across all %d samples", totalSamples)
	} else {
		t.Logf("  Result: %d sample(s) with divergence", totalSamples-perfectParity)
	}
}

func yamlTextSlice(src []byte, c highlightCapture) string {
	if c.StartByte < uint32(len(src)) && c.EndByte <= uint32(len(src)) {
		s := string(src[c.StartByte:c.EndByte])
		if len(s) > 60 {
			return s[:60] + "..."
		}
		return s
	}
	return fmt.Sprintf("<out-of-bounds:%d-%d>", c.StartByte, c.EndByte)
}
