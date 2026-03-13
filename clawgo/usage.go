package clawgo

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type LLMUsageRecord struct {
	InstanceID       int64
	UserID           int64
	RequestID        string
	ModelRequested   string
	ModelResolved    string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Status           string
	ErrorCode        string
	LatencyMs        int64
	CacheHit         bool
}

type SearchUsageRecord struct {
	InstanceID  int64
	UserID      int64
	RequestID   string
	Provider    string
	Query       string
	ResultCount int
	Status      string
	ErrorCode   string
	LatencyMs   int64
}

type UsageRecorder struct {
	db *sql.DB
}

func NewUsageRecorder(dsn string) *UsageRecorder {
	if dsn == "" {
		return &UsageRecorder{}
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Printf("usage recorder: failed to connect: %v", err)
		return &UsageRecorder{}
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &UsageRecorder{db: db}
}

func (u *UsageRecorder) Enabled() bool {
	return u.db != nil
}

func (u *UsageRecorder) Close() {
	if u.db != nil {
		u.db.Close()
	}
}

func (u *UsageRecorder) RecordLLM(r LLMUsageRecord) error {
	if u.db == nil {
		return nil
	}
	_, err := u.db.Exec(`INSERT INTO llm_usage_events
		(instance_id, user_id, request_id, model_requested, model_resolved,
		 prompt_tokens, completion_tokens, total_tokens, status, error_code, latency_ms, cache_hit, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`,
		r.InstanceID, r.UserID, r.RequestID, r.ModelRequested, r.ModelResolved,
		r.PromptTokens, r.CompletionTokens, r.TotalTokens, r.Status, r.ErrorCode, r.LatencyMs, r.CacheHit)
	if err != nil {
		log.Printf("usage recorder: llm insert failed: %v", err)
		return err
	}
	go u.upsertDaily(r.InstanceID, r.UserID, r.Status == "error", true, r.PromptTokens, r.CompletionTokens, r.TotalTokens)
	return nil
}

func (u *UsageRecorder) RecordSearch(r SearchUsageRecord) error {
	if u.db == nil {
		return nil
	}
	query := r.Query
	if len(query) > 512 {
		query = query[:512]
	}
	_, err := u.db.Exec(`INSERT INTO search_usage_events
		(instance_id, user_id, request_id, provider, query, result_count, status, error_code, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`,
		r.InstanceID, r.UserID, r.RequestID, r.Provider, query, r.ResultCount, r.Status, r.ErrorCode, r.LatencyMs)
	if err != nil {
		log.Printf("usage recorder: search insert failed: %v", err)
		return err
	}
	go u.upsertDaily(r.InstanceID, r.UserID, r.Status == "error", false, 0, 0, 0)
	return nil
}

func (u *UsageRecorder) upsertDaily(instanceID, userID int64, isError, isLLM bool, promptTokens, completionTokens, totalTokens int64) {
	if u.db == nil {
		return
	}
	date := time.Now().Format("2006-01-02")

	if isLLM {
		errInc := int64(0)
		if isError {
			errInc = 1
		}
		_, err := u.db.Exec(`INSERT INTO usage_dailies (instance_id, user_id, date, llm_requests, llm_prompt_tokens, llm_completion_tokens, llm_total_tokens, llm_errors, updated_at)
			VALUES (?, ?, ?, 1, ?, ?, ?, ?, NOW())
			ON DUPLICATE KEY UPDATE
				llm_requests = llm_requests + 1,
				llm_prompt_tokens = llm_prompt_tokens + VALUES(llm_prompt_tokens),
				llm_completion_tokens = llm_completion_tokens + VALUES(llm_completion_tokens),
				llm_total_tokens = llm_total_tokens + VALUES(llm_total_tokens),
				llm_errors = llm_errors + VALUES(llm_errors),
				updated_at = NOW()`,
			instanceID, userID, date, promptTokens, completionTokens, totalTokens, errInc)
		if err != nil {
			log.Printf("usage recorder: daily upsert failed: %v", err)
		}
	} else {
		errInc := int64(0)
		if isError {
			errInc = 1
		}
		_, err := u.db.Exec(`INSERT INTO usage_dailies (instance_id, user_id, date, search_requests, search_errors, updated_at)
			VALUES (?, ?, ?, 1, ?, NOW())
			ON DUPLICATE KEY UPDATE
				search_requests = search_requests + 1,
				search_errors = search_errors + VALUES(search_errors),
				updated_at = NOW()`,
			instanceID, userID, date, errInc)
		if err != nil {
			log.Printf("usage recorder: daily upsert failed: %v", err)
		}
	}
}
