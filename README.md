# snappy - a go package and client for the Snapmaker 2.0 A350

## Introduction

With the eventual goal of making Snapmaker operations scriptable, this
package is intended to provied a Go API for the Snapmaker 2 A350
machine.

## Running the example

```
$ git clone https://github.com/tinkerator/snappy.git
$ cd snappy
$ go build examples/snappy
```

In this directory, follow these instructions to generate your
`snapmaker.config` file. Preparation, under Linux, assumes you have
used Luban at least once to connect to your machine.

```
$ token=$(grep token ~/.config/snapmaker-luban/machine.json|tail -1|cut -d'"' -f4); \
  address=$(grep address ~/.config/snapmaker-luban/machine.json|cut -d'"' -f4) \
  ; echo "{\"Address\":\"$address\", \"Token\":\"$token\"}" > snapmaker.config
```

Then try to run the command:
```
$ ./snappy
```

For command line options:
```
$ ./snappy --help
```

## Protocol

The Snapmaker uses a plain URL/FORM API with JSON style return
values. Learning the command set is via `tcpdump`-ing the exchange
between Luban and the Snapmaker 2.0 A350 device.

Some `tcpdump` commands to help navigate:

- Gather a pcap dump (note, the A350 network traffic is to port 8080):
```
sudo tcpdump -w snap-$(date +%s).pcap -i eth0 port 8080
```

- View all of the text exchange:
```
tcpdump -qns 0 -A -r snap-1743357727.pcap | less
```

- Quickly scan for commands of interest:
```
tcpdump -qns 0 -A -r snap-1743357727.pcap | grep -E '(POST|GET)' | less
```

## TODO

Add more functionality. Currently, all the code can do is Home a
device with the 1.6W laser head installed. Nothing else has been
tested.

## License

The `snappy` package and examples are distributed with the same BSD
3-clause [license](LICENSE) as that used by
[golang](https://golang.org/LICENSE) itself.

## Requesting features and reporting bugs

This is a hobby project. No support should be expected. However, if
you want to suggest a feature, or find a bug, please use the github
[snappy bug tracker](https://github.com/tinkerator/snappy/issues).
