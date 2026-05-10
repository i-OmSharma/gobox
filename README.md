# veil : Container Runtime GO

Custom container runtime build in complete Golang. 

Usig Linux premitives

* NameSpaces
* cgroups
* OverlayFS

###  veil can perform this:

* create
* run 
* ps
* delete
* pull
* push

## Project Structure

```
  veil
    ├── cmd
    │   └── veil
    │       └── main.go
    ├── veil
    ├── go.mod
    ├── internal
    │   ├── cgroup
    |   |     └──cgroup.go
    │   ├── container
    │   │   └── container.go 
    │   ├── image
    │   ├── namespace
    │   ├── network
    │   └── overlayfs
    └── README.md

```

- internel/container/container.go <- <b>Orchestrtation Layer