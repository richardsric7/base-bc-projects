// Package cache provides a tiny key/value caching abstraction so the rest of
// the codebase never imports a Redis client directly. Swap NewRedisCache for
// another backend without touching a single caller.
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache is the interface every component depends on.
type Cache interface {
	Get(key string) (string, bool)
	Set(key string, value string, ttl time.Duration)
	Delete(key string)
}

// noopCache is used when caching is disabled via config; every call is a
// harmless no-op so callers never need an "if cache enabled" branch.
type noopCache struct{}

func (noopCache) Get(string) (string, bool)         { return "", false }
func (noopCache) Set(string, string, time.Duration) {}
func (noopCache) Delete(string)                     {}

// NewNoopCache returns a Cache that stores nothing.
func NewNoopCache() Cache { return noopCache{} }

// RedisCache is a thin wrapper around go-redis.
type RedisCache struct {
	client *redis.Client
	ctx    context.Context
}

// NewRedisCache dials Redis and verifies connectivity with a PING.
func NewRedisCache(host, port, password string) (Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     host + ":" + port,
		Password: password,
	})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &RedisCache{client: client, ctx: ctx}, nil
}

func (r *RedisCache) Get(key string) (string, bool) {
	val, err := r.client.Get(r.ctx, key).Result()
	if err != nil {
		return "", false
	}
	return val, true
}

func (r *RedisCache) Set(key string, value string, ttl time.Duration) {
	r.client.Set(r.ctx, key, value, ttl)
}

func (r *RedisCache) Delete(key string) {
	r.client.Del(r.ctx, key)
}
