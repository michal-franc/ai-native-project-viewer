.PHONY: build demo clean

build:
	go build -o issue-viewer .

demo: build
	./issue-viewer -config demo/projects.yaml -port 8080

clean:
	rm -f issue-viewer
