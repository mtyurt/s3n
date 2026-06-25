# s3n

Simple S3 object navigator

# How to install

- From source code:

```bash

go build -o /usr/local/bin/s3n .
```

- With `go install`:

```bash
go install github.com/mtyurt/s3n@latest
```


# How to use

```bash

# make sure proper AWS credentials are configured
s3n <bucket-name>

```

# Features

1. List all objects, navigate into virtual directories using `enter` and `backspace` (hit `?` for all hotkeys)
2. View object content with `enter` using `less` command
3. Edit object content with `ctrl+e` using `$EDITOR` envvar
4. Add a new object with `ctrl+a` and edit it
5. Delete an object with `ctrl+d` (asks for confirmation)
6. Filter loaded objects with `/`; while filtering press `ctrl+s` to search the whole bucket server-side using the typed text as prefix (`backspace`/back exits search)
7. Load the next page of objects with `n` when a directory has more than 100 objects

# How to test locally

- Start localstack from docker-compose

```
docker-compose up -d
```

- Run create some dummy files to test

```
make createall
```


- Run in local with `make localrun` and enjoy!
