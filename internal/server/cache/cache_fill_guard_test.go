package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 失效只删"当时已存在"的条目，拦不住在途 fetch。若回填不校验代次，旧值会在失效
// 之后被写回并长期驻留（settings 域 TTL 长达 24h），使失效形同虚设——表现为
// "管理员写成功、后续读取仍是旧值"。
func TestGetWithTTL_FillStartedBeforeInvalidateIsDiscarded(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)

	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	callerDone := make(chan struct{})

	go func() {
		defer close(callerDone)
		_, _ = c.GetWithTTL(context.Background(), "k", time.Hour,
			func(context.Context, string) (int, error) {
				close(fetchStarted)
				<-releaseFetch
				return 1, nil // 失效前的旧值
			})
	}()

	<-fetchStarted
	c.Invalidate("k") // 回填尚未落地时发生失效
	close(releaseFetch)
	<-callerDone

	_, ok := c.GetIfPresent("k")
	require.False(t, ok, "失效发生在回填之前，该回填必须被丢弃，不得把旧值留在缓存里")
}

// Clear 同理：清空发生在回填之前时，该回填不得把旧值写回。
func TestGetWithTTL_FillStartedBeforeClearIsDiscarded(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	c.SetWithTTL("k", 0, time.Nanosecond) // 过期占位，让 fetch 成为唯一路径

	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	callerDone := make(chan struct{})

	go func() {
		defer close(callerDone)
		_, _ = c.GetWithTTL(context.Background(), "other", time.Hour,
			func(context.Context, string) (int, error) {
				close(fetchStarted)
				<-releaseFetch
				return 7, nil
			})
	}()

	<-fetchStarted
	c.Clear() // 回填尚未落地时清空
	close(releaseFetch)
	<-callerDone

	_, ok := c.GetIfPresent("other")
	require.False(t, ok, "清空发生在回填之前，该回填必须被丢弃")
}

// 无失效发生时，回填必须正常落地——守卫不能把正常路径也拦掉。
func TestGetWithTTL_FillStillLandsWithoutInvalidation(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	ctx := context.Background()

	fetches := 0
	first, err := c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
		fetches++
		return 42, nil
	})
	require.NoError(t, err)
	require.Equal(t, 42, first)

	second, err := c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
		fetches++
		return 99, nil
	})
	require.NoError(t, err)
	require.Equal(t, 42, second, "第二次必须命中缓存，返回首次回填的值")
	require.Equal(t, 1, fetches, "缓存命中时不应再执行 fetch")
	require.Equal(t, 1, c.Len(), "无失效时回填应正常写入")
}

// Clear 之后的新 Get 不得加入清空前的 in-flight fetch：gen 守卫只阻止回填，
// 若不把代次编进 flight key，后到的调用者会拿到旧 fetch 的返回值。
func TestGetWithTTL_PostClearDoesNotJoinInFlightFetch(t *testing.T) {
	t.Parallel()
	c := NewTTLCache[string, int](time.Hour)
	ctx := context.Background()

	fetchAStarted := make(chan struct{})
	releaseA := make(chan struct{})
	aResult := make(chan int, 1)

	aErr := make(chan error, 1)
	go func() {
		v, err := c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
			close(fetchAStarted)
			<-releaseA
			return 1, nil
		})
		aErr <- err
		aResult <- v
	}()

	<-fetchAStarted
	c.Clear()

	bFetchStarted := make(chan struct{})
	bDone := make(chan struct{})
	var (
		v   int
		err error
	)
	go func() {
		defer close(bDone)
		v, err = c.GetWithTTL(ctx, "k", time.Hour, func(context.Context, string) (int, error) {
			close(bFetchStarted)
			return 2, nil
		})
	}()
	select {
	case <-bDone:
	case <-time.After(2 * time.Second):
		t.Fatal("清空后的 Get 仍在等待清空前的 in-flight fetch")
	}
	require.NoError(t, err)
	require.Equal(t, 2, v, "清空后的调用必须自己回源，不得拿到清空前 in-flight 的旧值")
	select {
	case <-bFetchStarted:
	default:
		t.Fatal("清空后的 Get 加入了清空前的 in-flight fetch")
	}

	got, ok := c.GetIfPresent("k")
	require.True(t, ok)
	require.Equal(t, 2, got, "后到的回填应按新代次写入")

	close(releaseA)
	select {
	case err := <-aErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("清空前的 Get 未在超时内返回")
	}
	require.Equal(t, 1, <-aResult)
	got, ok = c.GetIfPresent("k")
	require.True(t, ok)
	require.Equal(t, 2, got, "清空前的回填不得覆盖新代次已写入的值")
}
