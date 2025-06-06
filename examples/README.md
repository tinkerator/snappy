# The `snappy` tool

The `snappy` command line tool demonstrates features of the
`"zappem.net/pub/net/snappy"` package for driving a Snapmaker A350
machine. This README covers example ways to use the tool.

If you have any questions about the tool, or want to request a
feature, please use the Github [bug tracker for this
package](https://github.com/tinkerator/snappy/issues).

## Homing the device

Before you can command the device, you need to home it. With the
`snappy` tool, you can do this.

```
$ ./snappy --home
```

If you have an enclosure, you can also combine this operation with
enabling the enclosure fan at 100% (fumes from using the machine can
be unpleasant and the device provides this feature to help exhaust
them):

```
$ ./snappy --home --fan=100
```

## Adjusting the position of the tool head

Once you have homed the device, in the current coordinate system, you
can direct it to move to an absolute position (**CAUTION:** this is
usually a safe position after homing, but may not be if you have
something large on the work platform):

```
$ ./snappy --x 192.5 --y 170 --z 113 --move
```

It is often more convenient to move relative to the current
location. You can observe the current location as follows:

```
$ ./snappy --locate
2025/05/25 07:29:27 at (27.67,-38.21,263.10) offset=(-135.37,-102.45,-49.90)
```

The `offset` entry here captures the machine coordinates of the work
origin. This origin is a useful concept for adjusting to the details
of your system and project. Each tool head has different needs based
on the relative offset of the business end of the tool and the
machine's movement mechanism. Practically, you _nudge_ the head into
the desired position with commands like this:

```
$ ./snappy --nudge-x 3
$ ./snappy --nudge-y 4
$ ./snappy --nudge-z -0.5
```

You can combine these command line options to perform non-Manhatten
movement too, for example:

```
$ ./snappy --nudge-x 3 --nudge-y 4 --nudge-z -0.5
```

The provided numbers are in units of mm and looking at the
machine from the front:

- Positive X nudges move the head to the right. Negative X nudges to the left.
- Positive Y nudges conceptually move the head away from you towards
  the back of the machine (they actually move the working surface
  towards you and keep the head fixed). Negative Y nudges move the
  head towards you (actually move the working surface away from you).
- Positive Z nudges move the head upwards, away from the working
  surface. Negative Z nudges move the head towards the working surface.

You can provide floating point numbers for these nudges, and the
machine is quite precise. For example, `./snappy --nudge-z -0.05` will
make a 50 micrometer movement.

## Setting the work origin

For working with the laser(s) and CNC bits, you are typically
operating on the surface of something else. Snapmaker provides some
strategies for adjusting to this, but it possible to use the `snappy`
tool to set things up fairly efficiently.

**CAUTION** When changing the tool head, the system may be confused
  into running the head at full power the moment you connect the power
  to it. This is really unfortunate! **SO ALWAYS POWER THE SYSTEM DOWN
  BEFORE ATTACHING ANY OF THE TOOLHEADS!!**

This section has a subsection for each tool used. Over time, we'll
likely include more subsections here.

### The 1.6W blue laser tool head

- Tool reports as: `TOOLHEAD_LASER_1`
- File format (.nc): `levelOneLaserToolheadForSM2`
- Has camera: Yes

**CAUTION** Using the laser **REQUIRES USE OF THE EYE PROTECTION
  GLASSES**. The enclosure for the Snapmaker looks similar in color to
  these goggles, but based on online sources and the author's
  experience, the laser light does NOT SEEM TO BE APPROPRIATELY
  FILTERED BY THIS ENCLOSURE. The enclosure also has many metalic
  surfaces and _gaps_ through which laser light can and does find a
  way out. **Don't risk eye damage, and DO wear the glasses supplied
  with the laser**.

To calibrate this head, use `--nudge-{x,y,z}` commands to lower the
head until the lower end of the lens cylinder physically touches the
surface of the calibration card on the work surface. Next `--nudge-z
5.6` to raise the tool head up above the surface. The value `5.6` (mm)
is the calibrated value of the author's 1.5W laser device. Yours might
be different.

Once in this position, enter the command:

```
$ ./snappy --set-origin
$ ./snappy --locate
2025/05/25 11:54:13 at (0.00,0.00,0.00) offset=(-135.37,-102.45,-49.90)
```

This will set the work coordinate system relative to the current point
of the laser. For the laser, if `5.6` is a good setting for your tool
head, while using the same surface you should not need to change the Z
value (height) of the tool unless you have to `--home` the device
again. Generally, `--home` resets the work coordinate system.

However, you will likely need to reposition the head in the X & Y
position, with `--nudge-{x,y}` adjustments. Once you get the device
into the preferred location for working, set the final work origin,
with `./snappy --set-origin` again.

Once the work origin is set, you can always return the tool head to it
with the following command:

```
$ ./snappy --goto-origin
```

Accurately adjusting to the desired work origin can be detailed and
frustrating process. In some cases, adjusting by eye is _good
enough_. If you desire more accuracy, there are a few methods that can
be performed with this laser head. They include:

#### Using the lowest power light setting for the laser

TODO this turns on the laser at a low light setting, and you visually
  align it with some reference point on the surface you consider the
  origin point. Looking at the laser, no matter how low power it is
  at, however, seems questionable.

#### Using the camera for alignment

TODO this uses a test pattern, and then compares a photo of that
  pattern to compute the relative offset of the center of a photo and
  the center of the test pattern.

#### Using precisely placed reference points

TODO this is useful when aligning the CNC and the Laser module work
origins. Where the reference points are CNC drilled holes, this method
can also be used to align the laser on both sides of a surface. For
example, when working with a double sided PCB.

### The 10W blue laser tool head

- Tool reports as: `?`
- File format (.nc): `levelTwoLaserToolheadForSM2`
- Has camera: Yes

TODO

### The 2W IR laser tool head

- Tool reports as: `?`
- File format (.nc): `?`
- Has camera: No

TODO

### The 50W (default) CNC tool head

- Tool reports as: `?`
- File format (.cnc): `standardCNCToolheadForSM2`
- Has camera: No

TODO

### The 200W CNC tool head

- Tool reports as: `?`
- File format (.cnc): `?`
- Has camera: No
