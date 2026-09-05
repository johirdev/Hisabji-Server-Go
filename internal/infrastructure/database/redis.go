package database

import "github.com/redis/go-redis/v9"

func OpenRedis(url string) (*redis.Client, error) {
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(options), nil
}
