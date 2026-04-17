DOKKU_APP ?= goanon
DOKKU_DEPLOY_URL ?= dokku@dev.lookingfora.name

dokku-deploy:
	git push --atomic $(DOKKU_DEPLOY_URL):$(DOKKU_APP) $(shell git rev-parse HEAD):refs/heads/master --force

dokku-build:
	docker build -t goanon-server:latest -f misc/dokku/Dockerfile .

dokku-run: dokku-build
	docker run -it --rm -v $(PWD)/models:/app/models:ro -p 8080:8080 goanon-server:latest