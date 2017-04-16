# DTSync

[![Build Status](https://travis-ci.org/aykevl/dtsync.svg?branch=master)](https://travis-ci.org/aykevl/dtsync)

This is a two-way directory synchronizer similar to
[Unison](https://www.cis.upenn.edu/~bcpierce/unison/) and designed as a possible
replacement. [Read my blog post](https://aykevl.nl/2017/04/dtsync) for an
overview.

More precisely, it is two things at the same time:

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
the paper. For more background and a hopefully easier-to-grasp explanation,
[read my blog post about the
algorithm](https://aykevl.nl/2017/04/concise-version-vectors).

[Unison](https://www.cis.upenn.edu/~bcpierce/unison/) was the big inspiration
for dtsync, and you should probably use it instead if you don't run into it's
limitations as it is much better tested.

Goals and features of DTSync over other synchronization tools like Unison,
[Dropbox](https://www.dropbox.com/) and [BitTorrent
Sync](https://www.resilio.com/individuals/):

  * Synchronize an arbitrary number of replicas in two directions, _without_
    central coordination or need to reach those replicas. Without false
    positives (or false negatives) in the conflict detection algorithm.
  * Be rock-solid.
  * Support less common file types like symlinks that some systems don't
    support.
  * Run on any platform. Be as versatile as the venerable
    [rsync](https://rsync.samba.org/). Certainly don't require a GUI or a
    particular desktop environment as part of the core program. At the same
    time, don't lock to the CLI like rsync did.
  * Do not have some central storage or coordination server. Do not depend on a
    fast internet speed, for that matter. That means, synchronize directly
    between endpoints instead of going through a central server.
  * Do not impose arbitrary limits on file sizes.

The major inspiration of course comes from rsync and Unison, as you might have
guessed by now.

To actually make something useful I had to limit myself in what features to add
initially. I hope to lift these restrictions in the future.

  * There is no rename/move support. This is something I would really like to
    have, as it can make synchronization a lot faster and even prevent some
    conflicts.
  * There is no hardlink support. This is something I don't care so much about,
    but hardlinks must be taken into account for proper move support (using
    inode numbers). No hardlink support means that hardlinks are ignored. DTSync
    will work just fine with hardlinks, but they'll simply be broken on any
    update.
  * As there is many-way update detection anyway via a distributed algorithm, it
    shouldn't be too hard to add 3-way (or even n-way) synchronization. Bringing
    me to the next point...
  * When there is 3-way synchronization, it might be possible to implement a
    merge algorithm. For example, consider two ZIP files. They may both be
    updated, but the updates itself may be for separate files. With a third
    replica in the mix, we can detect which files these are and merge the
    changes. Taking this a bit further, we might actually automatically merge
    any text document via `diff` and `patch`. Or even .odt or .docx files:
    they're just a bunch of XML files inside ZIP files that should be fairly
    straightforward (but certainly not easy!) to merge.

## Installing

Installing is quite simple if you have [golang](https://golang.org/) installed.
Just run the following command:

    go get github.com/aykevl/dtsync/dtsync

The resulting `dtsync` binary will be stored in your `$HOME/bin` directory.

You will need a few dependencies:

  * The [Go compiler](https://golang.org/dl/).
  * [`librsync-dev`](http://librsync.sourceforge.net/)
  * GObject-introspection for Python 3 (see
    [Installing](http://python-gtk-3-tutorial.readthedocs.io/en/latest/install.html)).
    This is only needed for the GTK3 frontend.

The GTK3 frontend can be found in
`$HOME/src/github.com/aykevl/dtsync/gtk/dtsync.py`. It needs the `dtsync` binary
in your search path (`$PATH`), which will often be the case after installing
`dtsync` inside `~/bin`. If not, you can add it. For example, in bash:

    PATH=$PATH:~/bin python3 ~/src/github.com/aykevl/dtsync/gtk/dtsync.py


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


## Program flow

The main flow of using dtsync is as follows, very similar to Unison:

  * Scan a pair of replicas (directories on a filesystem). These can be local,
    remote, or both.
  * Reconcile both trees that have diverged since the last sync. If this is the
    first synchronization, all files only on one side are seen as new and all
    files on both sides that differ are seen as conflicts.
  * Present a list of changes, or 'jobs' to apply. For each file or directory,
    The UI lists the modification state on each side (unmodified, updated, new,
    deleted, etc.). Also a direction is presented: from updated to unmodified,
    from new to nonexistent, or from nonexistent to unmodified. The algorithm is
    conservative: it won't by default present a direction for anything that
    isn't obvious. E.g. two modified files won't have a default direction.
  * The user can now choose what to do with that list. For each job, the
    direction can be set to left-to-right, right-to-left, or pass/ignore.
  * As nearly the last step, the sync can be applied. All jobs that aren't set
    to pass will be applied, usually a bunch at the same time (8 jobs at the
    same time at the time of writing this).
  * The last step is that both replicas will be marked as 'finished', and
    incorporating all changes from the other replica. That is, if all changes
    are applied.


## Issues

This tool seems to be stable, although unfinished. I am not aware of any issues
that will eat your data. I use it for some of my synchronization needs. But be
careful to check what it does exactly.

Things that are left to do, roughly in order of importance:

  * Add proper profile support. There is some initial support, but it needs to
    be expanded to all other options currently stored inside .dtsync files.
  * Properly deal with errors. Often they'll completely stop the
    synchronization. The UI must clearly indicate what went wrong so the user
    can either fix it, or maybe it'll be fixed on a second synchronization.
  * Deal with incomplete syncs. Mark the files that haven't completed
    synchronization as such, so that future syncs can use the correct version
    vector for the files and prevent false positives in conflict handling.
  * Some performance optimization. Investigate alternatives like sha256 for
    hashing, as such algorithms tend to have [better hardware support](https://blog.minio.io/accelerating-sha256-by-100x-in-golang-on-arm-1517225f5ff4).
  * Add many more options, as needed, so dtsync will be a better competitor to
    Unison.
