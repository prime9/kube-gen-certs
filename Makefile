REG=andrewstuart
IMAGE := $(shell basename $(PWD))

.PHONY: build push deploy

TAG=$(REG)/$(IMAGE)

$(IMAGE): *.go
	go get
	go build -o $(IMAGE)

build: $(IMAGE)
	-upx $(IMAGE)
	docker build -t $(TAG) .

push: build
	docker push $(TAG)

deploy: push
	kubectl delete po -l app=kube-gen-certs
