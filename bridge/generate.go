package bridge

//go:generate go run ../internal/specgen -bridge ../spec/protocol/v1
//go:generate go run ../internal/specgen -rust ../spec/protocol/v1 ../rust/app/src
//go:generate go run ../internal/specgen -python ../spec/protocol/v1 ../python/synopkgland_app
