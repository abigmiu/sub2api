package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type playgroundImageTaskRepository struct {
	db *sql.DB
}

func NewPlaygroundImageTaskRepository(db *sql.DB) service.PlaygroundImageTaskRepository {
	return &playgroundImageTaskRepository{db: db}
}

func (r *playgroundImageTaskRepository) Create(ctx context.Context, task *service.PlaygroundImageTask) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO playground_image_tasks
		(id, user_id, status, request_path, request_content_type, request_body, error_message, result_json, created_at, started_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		task.ID,
		task.UserID,
		string(task.Status),
		task.RequestPath,
		task.RequestContentType,
		task.RequestBody,
		task.ErrorMessage,
		nullBytes(task.ResultJSON),
		task.CreatedAt,
		nullTime(task.StartedAt),
		nullTime(task.FinishedAt),
	)
	return err
}

func (r *playgroundImageTaskRepository) GetByID(ctx context.Context, id string) (*service.PlaygroundImageTask, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, user_id, status, request_path, request_content_type, request_body, error_message, result_json, created_at, started_at, finished_at
		FROM playground_image_tasks WHERE id = $1`, id)

	task := &service.PlaygroundImageTask{}
	var resultJSON []byte
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	if err := row.Scan(
		&task.ID,
		&task.UserID,
		&task.Status,
		&task.RequestPath,
		&task.RequestContentType,
		&task.RequestBody,
		&task.ErrorMessage,
		&resultJSON,
		&task.CreatedAt,
		&startedAt,
		&finishedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrPlaygroundImageTaskNotFound
		}
		return nil, err
	}
	task.ResultJSON = resultJSON
	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		task.FinishedAt = &finishedAt.Time
	}
	return task, nil
}

func (r *playgroundImageTaskRepository) UpdateStatus(ctx context.Context, id string, status service.PlaygroundImageTaskStatus, errorMessage string, resultJSON []byte, startedAt, finishedAt *time.Time) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE playground_image_tasks
		SET status = $2, error_message = $3, result_json = $4, started_at = COALESCE($5, started_at), finished_at = $6
		WHERE id = $1`,
		id,
		string(status),
		errorMessage,
		nullBytes(resultJSON),
		nullTime(startedAt),
		nullTime(finishedAt),
	)
	return err
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}
