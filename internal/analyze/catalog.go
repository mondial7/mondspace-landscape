package analyze

import (
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/graph"
)

// catalogEntry maps an import (exact or by prefix) to an external system.
type catalogEntry struct {
	langs    []string // languages the import applies to
	prefixes []string // import path prefixes ("pkg" matches "pkg" and "pkg/…", "pkg.sub")
	ext      External
}

var (
	jsLangs = []string{"javascript", "typescript"}
	goLangs = []string{"go"}
	pyLangs = []string{"python"}
	swLangs = []string{"swift"}
)

func ext(id, name string, kind ExternalKind) External {
	return External{ID: id, Name: name, Kind: kind}
}

// catalog is intentionally plain data: extending recognition of a library
// is a one-line change.
var catalog = []catalogEntry{
	// Databases
	{goLangs, []string{"modernc.org/sqlite", "github.com/mattn/go-sqlite3", "github.com/glebarez/go-sqlite", "zombiezen.com/go/sqlite"}, ext("sqlite", "SQLite", ExtDatabase)},
	{jsLangs, []string{"sqlite3", "better-sqlite3", "sqlite"}, ext("sqlite", "SQLite", ExtDatabase)},
	{pyLangs, []string{"sqlite3", "aiosqlite"}, ext("sqlite", "SQLite", ExtDatabase)},
	{goLangs, []string{"github.com/lib/pq", "github.com/jackc/pgx"}, ext("postgres", "PostgreSQL", ExtDatabase)},
	{jsLangs, []string{"pg", "postgres", "@neondatabase/serverless"}, ext("postgres", "PostgreSQL", ExtDatabase)},
	{pyLangs, []string{"psycopg2", "psycopg", "asyncpg"}, ext("postgres", "PostgreSQL", ExtDatabase)},
	{goLangs, []string{"github.com/go-sql-driver/mysql"}, ext("mysql", "MySQL", ExtDatabase)},
	{jsLangs, []string{"mysql", "mysql2"}, ext("mysql", "MySQL", ExtDatabase)},
	{pyLangs, []string{"pymysql", "mysqlclient", "MySQLdb"}, ext("mysql", "MySQL", ExtDatabase)},
	{goLangs, []string{"go.mongodb.org/mongo-driver"}, ext("mongodb", "MongoDB", ExtDatabase)},
	{jsLangs, []string{"mongodb", "mongoose"}, ext("mongodb", "MongoDB", ExtDatabase)},
	{pyLangs, []string{"pymongo", "motor"}, ext("mongodb", "MongoDB", ExtDatabase)},
	{goLangs, []string{"github.com/redis/go-redis", "github.com/go-redis/redis", "github.com/gomodule/redigo"}, ext("redis", "Redis", ExtDatabase)},
	{jsLangs, []string{"redis", "ioredis", "@upstash/redis"}, ext("redis", "Redis", ExtDatabase)},
	{pyLangs, []string{"redis"}, ext("redis", "Redis", ExtDatabase)},
	{goLangs, []string{"go.etcd.io/bbolt", "github.com/dgraph-io/badger"}, ext("kvstore", "Embedded KV store", ExtDatabase)},
	{goLangs, []string{"gorm.io/gorm", "github.com/jmoiron/sqlx", "database/sql"}, ext("sql", "SQL database", ExtDatabase)},
	{jsLangs, []string{"@prisma/client", "drizzle-orm", "knex", "typeorm", "sequelize"}, ext("sql", "SQL database", ExtDatabase)},
	{pyLangs, []string{"sqlalchemy", "peewee", "django.db"}, ext("sql", "SQL database", ExtDatabase)},
	{swLangs, []string{"CoreData", "SwiftData", "GRDB", "SQLite"}, ext("local-store", "On-device store", ExtDatabase)},

	// Queues / messaging
	{goLangs, []string{"github.com/nats-io/nats.go"}, ext("nats", "NATS", ExtQueue)},
	{goLangs, []string{"github.com/segmentio/kafka-go", "github.com/IBM/sarama", "github.com/Shopify/sarama"}, ext("kafka", "Kafka", ExtQueue)},
	{jsLangs, []string{"kafkajs"}, ext("kafka", "Kafka", ExtQueue)},
	{goLangs, []string{"github.com/rabbitmq/amqp091-go", "github.com/streadway/amqp"}, ext("rabbitmq", "RabbitMQ", ExtQueue)},
	{jsLangs, []string{"amqplib", "bullmq", "bull"}, ext("queue", "Message queue", ExtQueue)},
	{pyLangs, []string{"pika", "celery", "kafka"}, ext("queue", "Message queue", ExtQueue)},

	// Cloud
	{goLangs, []string{"github.com/aws/aws-sdk-go", "github.com/aws/aws-sdk-go-v2"}, ext("aws", "AWS", ExtCloud)},
	{jsLangs, []string{"@aws-sdk", "aws-sdk"}, ext("aws", "AWS", ExtCloud)},
	{pyLangs, []string{"boto3", "botocore"}, ext("aws", "AWS", ExtCloud)},
	{goLangs, []string{"cloud.google.com/go", "google.golang.org/api"}, ext("gcp", "Google Cloud", ExtCloud)},
	{jsLangs, []string{"@google-cloud", "googleapis"}, ext("gcp", "Google Cloud", ExtCloud)},
	{pyLangs, []string{"google.cloud"}, ext("gcp", "Google Cloud", ExtCloud)},
	{goLangs, []string{"github.com/Azure/azure-sdk-for-go"}, ext("azure", "Azure", ExtCloud)},
	{jsLangs, []string{"@azure"}, ext("azure", "Azure", ExtCloud)},
	{jsLangs, []string{"firebase", "firebase-admin"}, ext("firebase", "Firebase", ExtCloud)},
	{swLangs, []string{"FirebaseCore", "FirebaseFirestore", "FirebaseAuth"}, ext("firebase", "Firebase", ExtCloud)},
	{jsLangs, []string{"@supabase/supabase-js"}, ext("supabase", "Supabase", ExtCloud)},
	{swLangs, []string{"CloudKit"}, ext("icloud", "iCloud / CloudKit", ExtCloud)},

	// SaaS
	{goLangs, []string{"github.com/stripe/stripe-go"}, ext("stripe", "Stripe", ExtSaaS)},
	{jsLangs, []string{"stripe", "@stripe"}, ext("stripe", "Stripe", ExtSaaS)},
	{pyLangs, []string{"stripe"}, ext("stripe", "Stripe", ExtSaaS)},
	{swLangs, []string{"StoreKit"}, ext("appstore", "App Store (StoreKit)", ExtSaaS)},
	{goLangs, []string{"github.com/slack-go/slack"}, ext("slack", "Slack", ExtSaaS)},
	{jsLangs, []string{"@slack"}, ext("slack", "Slack", ExtSaaS)},
	{goLangs, []string{"github.com/google/go-github"}, ext("github", "GitHub API", ExtSaaS)},
	{jsLangs, []string{"@octokit"}, ext("github", "GitHub API", ExtSaaS)},
	{jsLangs, []string{"@sentry"}, ext("sentry", "Sentry", ExtSaaS)},
	{goLangs, []string{"github.com/getsentry/sentry-go"}, ext("sentry", "Sentry", ExtSaaS)},

	// AI
	{goLangs, []string{"github.com/anthropics/anthropic-sdk-go"}, ext("anthropic", "Anthropic API", ExtAI)},
	{jsLangs, []string{"@anthropic-ai/sdk"}, ext("anthropic", "Anthropic API", ExtAI)},
	{pyLangs, []string{"anthropic"}, ext("anthropic", "Anthropic API", ExtAI)},
	{goLangs, []string{"github.com/sashabaranov/go-openai", "github.com/openai/openai-go"}, ext("openai", "OpenAI API", ExtAI)},
	{jsLangs, []string{"openai"}, ext("openai", "OpenAI API", ExtAI)},
	{pyLangs, []string{"openai"}, ext("openai", "OpenAI API", ExtAI)},
	{goLangs, []string{"github.com/ollama/ollama/api"}, ext("ollama", "Ollama", ExtAI)},
	{jsLangs, []string{"ollama"}, ext("ollama", "Ollama", ExtAI)},
	{swLangs, []string{"FoundationModels", "CoreML"}, ext("on-device-ai", "On-device ML", ExtAI)},

	// Mail
	{goLangs, []string{"net/smtp"}, ext("smtp", "SMTP mail server", ExtMail)},
	{jsLangs, []string{"nodemailer", "@sendgrid", "resend"}, ext("mail", "Email service", ExtMail)},
	{pyLangs, []string{"smtplib"}, ext("smtp", "SMTP mail server", ExtMail)},

	// Platform
	{goLangs, []string{"os/exec"}, ext("os-processes", "OS processes", ExtPlatform)},
	{jsLangs, []string{"child_process", "node:child_process"}, ext("os-processes", "OS processes", ExtPlatform)},
	{pyLangs, []string{"subprocess"}, ext("os-processes", "OS processes", ExtPlatform)},
	{swLangs, []string{"UserNotifications"}, ext("notifications", "Push / local notifications", ExtPlatform)},
	{swLangs, []string{"HealthKit"}, ext("healthkit", "HealthKit", ExtPlatform)},
	{swLangs, []string{"Contacts", "EventKit"}, ext("personal-data", "Contacts / Calendar", ExtPlatform)},
	{swLangs, []string{"Photos", "PhotosUI"}, ext("photos", "Photo library", ExtPlatform)},
}

// Filesystem is the external representing files written to local disk.
var Filesystem = External{ID: "filesystem", Name: "Local filesystem", Kind: ExtStorage}

// EdgeKindFor returns the relationship kind used when code talks to an
// external system of the given kind.
func EdgeKindFor(k ExternalKind) graph.EdgeKind {
	switch k {
	case ExtDatabase, ExtQueue, ExtStorage:
		return graph.EdgeData
	case ExtHTTP, ExtSaaS, ExtAI, ExtCloud:
		return graph.EdgeHTTP
	default:
		return graph.EdgeUses
	}
}

// Lookup returns the external system an import refers to, if any.
func Lookup(lang, imp string) (External, bool) {
	for _, e := range catalog {
		if !contains(e.langs, lang) {
			continue
		}
		for _, p := range e.prefixes {
			if imp == p || strings.HasPrefix(imp, p+"/") || strings.HasPrefix(imp, p+".") {
				return e.ext, true
			}
		}
	}
	return External{}, false
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
