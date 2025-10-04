.PHONY: all build css clean run reload

all: build

css:
	npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --minify

build: css
	go build -o nhe .

run: build
	./nhe --db app.db serve

reload: build
	./nhe --db app.db load

clean:
	rm -f nhe
	rm -f static/css/output.css
	rm -f app.db test.db debug.log
