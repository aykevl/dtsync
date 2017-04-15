# Distributed Tree Synchronisation

[![Build Status](https://travis-ci.org/aykevl/dtsync.svg?branch=master)](https://travis-ci.org/aykevl/dtsync)

This is two things at the same time:

  * An algorithm that detects differences between two abstract trees. Currently,
    it only detects added, updated and removed files (and directories). A
    wishlist item is that it should also detect moves and hardlinks (two closely
    related things).
  * A file synchronizer implemented using this algorithm. Many replicas can be
    synchronized in a mesh form, with updates propagating from one replica to
    all the others.

Many of the ideas behind the algorithm are taken from something called [concise
version
vectors](https://scholar.google.nl/scholar?cluster=15694180381552406021). If you
want to understand the algorithm here, first read up on [version
vectors](https://en.wikipedia.org/wiki/Version_vector), then come back to read
the paper.

In short, you can use this as a replacement for
[Unison](https://www.cis.upenn.edu/~bcpierce/unison/). But, note that Unison has
many more features and is much better tested. Use Unison if you need to rely on
it.

## Installing

    go get github.com/aykevl/dtsync/dtsync

## Package overview

To make the whole system a bit more modular, I've split the software in various
packages:

| package  | description |
| -------- | ----------- |
| `tree/memory`<br>`tree/file`<br>`tree/remote` | Abstraction layer to various types of filesystems. In practice, only `tree/file` and `tree/remote` will be used, but more might be added in the future (e.g. things like MTP, sftp/sshfs, or things I haven't even thought about). `tree/memory` is only used to speed up testing. |
| `tree`   | Contains various interfaces and utility functions for the `tree/*` packages. |
| `dtdiff` | Contains the current tree state. Saves it to a file, loads it from a file, and contains methods to stream to/from a remote host. It also scans using a `tree/*` interface, and updates the current state accordingly. |
| `sync`   | Contains the actual algorithms for synchronization. It creates two `tree` interfaces, commands `dtsync` to scan it, and then compares both trees.  This results in a list of jobs that can be displayed to the user, changed in direction if necessary, and can be applied. |
| `dtsync` | Contains the actual command (the main package). It contains both a command line client and a client using [msgpack](https://msgpack.org/) on stdin/stdout. The latter can be used to develop GUIs, or maybe other interesting interfaces. |
| `gtk`    | A GTK3 frontend, using the msgpack interface. |

## Issues

This tool seems to be stable, although unfinished. I am not aware of any issues
that will eat your data. I use it for some of my synchronization needs. But be
careful to check what it does exactly.
