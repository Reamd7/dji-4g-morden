# Setting up a data connection over QMI interface using libqmi

> Source: <https://docs.sixfab.com/page/setting-up-a-data-connection-over-qmi-interface-using-libqmi>

Cellular modules that are based on the Qualcomm chipsets support the QMI interface. The [libqmi](https://www.freedesktop.org/wiki/Software/libqmi/) can be used to establish QMI interface for mini PCIe modules such as Quectel EC25, EG25-G, EC21, UC20-G and Telit LE910C1, LE910C4 with the [Raspberry Pi 3G/4G & LTE Base HAT](https://sixfab.com/product/raspberry-pi-base-hat-3g-4g-lte-minipcie-cards/).

This is a brief tutorial to establish the connection, reference sites are listed at the end of the tutorial.

Please note that the below tutorial assumes that you have already completed your hardware setup, if not please follow [getting started](https://docs.sixfab.com/docs/raspberry-pi-3g-4g-lte-base-hat-getting-started).

Before we start, check the compatibility of the module.

```
lsusb -t
```

should return driver information such as **qmi-wwan** (opensource) or **GobiNet** (provided by Qualcomm) as shown below. Both drivers work fine with the libqmi.

```
   |__ Port 4: Dev 6, If 4, Class=Vendor Specific Class, Driver=qmi_wwan, 480M
```

> ⚙️ **Module Configuration**
>
> Before running the PPP/QMI make sure the module is configured to the right settings.
>
> **For Quectel Modules:**
> `AT+QCFG="usbnet"` should return 0, otherwise, send `AT+QCFG="usbnet",0` then reboot the module after 10 seconds `AT+CFUN=1,1`
>
> **For Telit Modules:**
> `AT#USBCFG?` should return 0, otherwise, send `AT#USBCFG=0` then reboot the module after 10 seconds, `AT#REBOOT`
>
> For sending AT commands, you may check the [Sending AT Commands](https://docs.sixfab.com/page/sending-at-commands) tutorial.

> **Note:** In case of the driver error please refer to the driver guide of the modules.

First, install the required packages.

```
sudo apt update && sudo apt install libqmi-utils udhcpc
```

libqmi-utils installs two main utilities (qmi-cli tool and qmi-network helper script), these are used for interaction with the modem (for more details check `man qmi-cli`)

Now make sure the module is ready, this can be done using the following command.

```
sudo qmicli -d /dev/cdc-wdm0 --dms-get-operating-mode
```

This should return `online` if not try

```
sudo qmicli -d /dev/cdc-wdm0 --dms-set-operating-mode='online'
```

Now configure the network interface for the raw-ip protocol.

```
sudo ip link set wwan0 down
```

```
echo 'Y' | sudo tee /sys/class/net/wwan0/qmi/raw_ip
```

```
sudo ip link set wwan0 up
```

One can confirm the data format using

```
sudo qmicli -d /dev/cdc-wdm0 --wda-get-data-format
```

Once the wwan0 is up, connect the mobile network by changing the `apn='YOUR_APN'`, `username='YOUR_USERNAME'`, `password='YOUR_PASSWORD'` part of the line according to the information of your SIM & operator. If username and password are not required, delete those parameters.

```
sudo qmicli -p -d /dev/cdc-wdm0 --device-open-net='net-raw-ip|net-no-qos-header' --wds-start-network="apn='YOUR_APN',username='YOUR_USERNAME',password='YOUR_PASSWORD',ip-type=4" --client-no-release-cid
```

Lastly, configure the IP address and the default route with udhcpc.

```
sudo udhcpc -q -f -i wwan0
```

```
pi@raspberrypi:~ $ sudo udhcpc -q -f -i wwan0
udhcpc: started, v1.30.1
No resolv.conf for interface wwan0.udhcpc
udhcpc: sending discover
udhcpc: sending select for 100.67.114.164
udhcpc: lease of 100.67.114.164 obtained, lease time 7200
Too few arguments.
Too few arguments.
```

## Checking The Connection

Now check the assigned IP address and test the connection.

```
pi@raspberrypi:~ $ ifconfig wwan0
wwan0: flags=4305<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST>  mtu 1500
        inet 100.67.114.164  netmask 255.255.255.248  destination 100.67.114.164
        inet6 fe80::abc4:a1b5:5e84:92f2  prefixlen 64  scopeid 0x20<link>
        unspec 00-00-00-00-00-00-00-00-00-00-00-00-00-00-00-00  txqueuelen 1000  (UNSPEC)
        RX packets 3  bytes 640 (640.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 13  bytes 2694 (2.6 KiB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0
```

```
pi@raspberrypi:~ $ ping -I wwan0 -c 5 sixfab.com
PING sixfab.com (172.67.75.126) from 100.67.114.164 wwan0: 56(84) bytes of data.
64 bytes from 172.67.75.126 (172.67.75.126): icmp_seq=1 ttl=29 time=247 ms
64 bytes from 172.67.75.126 (172.67.75.126): icmp_seq=2 ttl=29 time=205 ms
64 bytes from 172.67.75.126 (172.67.75.126): icmp_seq=3 ttl=29 time=207 ms
64 bytes from 172.67.75.126 (172.67.75.126): icmp_seq=4 ttl=29 time=204 ms
64 bytes from 172.67.75.126 (172.67.75.126): icmp_seq=5 ttl=29 time=216 ms

--- sixfab.com ping statistics ---
5 packets transmitted, 5 received, 0% packet loss, time 8ms
rtt min/avg/max/mdev = 204.050/215.839/247.004/16.201 ms
```

> ✅ Enjoy your internet connection

## Troubleshooting

It is worth reading we recommend going through the section on troubleshooting as it covers the more common issues with establishing and maintaining a network connection. Please check out the [troubleshooting guide](https://docs.sixfab.com/docs/raspberry-pi-3g-4g-lte-base-hat-troubleshooting).

## Reference

1. <https://www.freedesktop.org/wiki/Software/libqmi/>
2. <https://www.raspberrypi.org/forums/viewtopic.php?f=36&t=224355&p=1703897&hilit=4g+module#p1703897>
3. [How to step by step set up a data connection over QMI interface using qmicli and in-kernel driver qmi_wwan in Linux?](https://techship.com/support/faq/how-to-step-by-step-set-up-a-data-connection-over-qmi-interface-using-qmicli-and-in-kernel-driver-qmi-wwan-in-linux/)
