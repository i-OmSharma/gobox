# gobox : Container Runtime GO

Custom container runtime build in complete Golang. 

Usig Linux premitives

* NameSpaces
* cgroups
* OverlayFS

###  gobox can perform this:

* create
* run 
* ps
* delete
* pull
* push

## Project Structure

```
  gobox
    ├── cmd
    │   └── gobox
    │       └── main.go
    ├── gobox
    ├── go.mod
    ├── internal
    │   ├── cgroup
    │   ├── container
    │   │   └── container.go 
    │   ├── image
    │   ├── namespace
    │   ├── network
    │   └── overlayfs
    └── README.md

```

- internel/container/container.go <- <b>Orchestrtation Layer