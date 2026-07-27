.PHONY: test test-all bench clean

test:
	go test -v -count=1 ./...

test-all:
	go test -v -tags bench -count=1 ./...

bench:
	go test -v -tags bench -run="TestMeasurePercentiles" ./bench/... | go run ./bench/report/gen_reports.go
	go test -v -tags bench -run="TestRetrievalRelevance|TestConsolidationQuality|TestPolishAccuracy" ./bench/...

clean:
	rm -rf reasoning-memory
