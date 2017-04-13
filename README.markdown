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

## Issues

This tool seems to be stable, although unfinished. I am not aware of any issues
that will eat your data. I use it for some of my synchronization needs. But be
careful to check what it does exactly.
