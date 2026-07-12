# Quectel EC25

> Source: <https://osmocom.org/projects/quectel-modems/wiki/EC25>

The Quectel EC25 is a LTE Modem Module manufactured by the Chinese Company Quectel. It is available as solder-type version but also as miniPCIe card.

It is based around the Qualcomm MSM 9x70 and runs an [OE based Linux distribution](https://osmocom.org/projects/quectel-modems/wiki/Qualcomm_OE_MSM) on its internal Cortex-A5 core. This Linux on Cortex-A5 is what implements the USB device that you see from the host PC!

Below testing has been made on an EC25-E Revision: EC25EFAR02A03M4G (according to ATI0 and the label on the device).

## serial console

In the EC25-E miniPCI that was analyzed, the serial console of bootloader and Linux appears to be active on pins 11+12 of the LGA module (DBG_RXD, DBG_TXD). The console is at 1.8V and at 115200bps.

You can use something like [the Osmocom Multi-Voltage UART](https://osmocom.org/projects/mv-uart/wiki) to interface an UART at 1.8V.

### Linux

this is the UART from the Linux point-of-view:

```
[    0.343979] msm_serial_hsl_probe: detected port #0 (ttyHSL0)
[    0.344034] msm_serial_hsl_probe: Bus scaling is disabled
[    0.344162] 78b3000.serial: ttyHSL0 at MMIO 0x78b3000 (irq = 153, base_baud = 460800) is a MSM
```

The boot command line arguments feature `console=ttyHSL0,115200,n8 earlycon=msm_hsl_uart,0x78b3000`

### Not enabled?

It seems like not all modules have the serial console enabled. It is yet TBD to figure out what can be done to enable/disable it.

In terms of pinctrl, an EC25 with enabled serial console shows the following from `/sys/kernel/debug/pinctrl/pinctrl-handles`:

```
device: 78b3000.serial current state: default
  state: default
    type: MUX_GROUP controller 1000000.pinctrl group: gpio8 (8) function: blsp_uart5 (16)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio9 (9) function: blsp_uart5 (16)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio8 (8) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio9 (9) 00010004 00020009
```

## enabling adb

### via serial console

access the serial console of the device and enter the following commands

```
echo 0 > /sys/class/android_usb/android0/enable
echo adb,diag,serial,rmnet > /sys/class/android_usb/android0/functions
echo 1 > /sys/class/android_usb/android0/enable
```

at this point the usb device re-enumerates on the PC and has now 6 instead of 5 interfaces, in the following order:

| Interface | Type | Driver | Purpose |
| --- | --- | --- | --- |
| 0 | adb | - | adbd on USB host |
| 1 | diag | - | diag software on host |
| 2 | serial | qcserial | GPS |
| 3 | serial | qcserial | AT commands |
| 4 | serial | qcserial | AT commands |
| 5 | rmnet | qmi_wwan | libqmi/qmicli |

See [Android_USB_Gadget](https://osmocom.org/projects/quectel-modems/wiki/Android_USB_Gadget) for more information on configuration options of the USB gadget.

**NOTE: If you use stock Linux, the drivers will have fixed assumptions as to which interface is used by what function! You need to patch your kernel to change that assumption, or ensure that the order of interfaces / interface numbers doesn't change**

## processes

### quectel_daemon

seems to be primarily concerned with voice routing / alsa codec related bits, including playback of ringtones

### atfwd_daemon

implements Quectel specific extensions to the AT command interpreter (ATCOP) using the QMI framework to register them in the modem processor. See [AT Commands](https://osmocom.org/projects/quectel-modems/wiki/AT_Commands).

### Quec_WIFI_CLI

### /usr/bin/time_daemon

- get time from modem via qmi
- get time from RTC

```
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_pre_init::Base = 0
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: ats_rtc_init: Time read from RTC -- year = 70, month = 0,day = 1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Value read from RTC seconds = 700000
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_init_config: ATS_RTC initialized
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_pre_init::Base = 1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]:  Storage Name: ats_1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Opening File: /data/time/ats_1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: time_persistent_memory_opr:Genoff Read operation
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_pre_init::Base = 2
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]:  Storage Name: ats_2
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Opening File: /data/time/ats_2
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: time_persistent_memory_opr:Genoff Read operation
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Unable to open filefor read
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_post_init:Error in accessing storage
...
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_init_config: Other bases initilized, exiting genoff_init
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_opr: Base = 1, val = 198101071560715, operation = 1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff get for 1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: rtc_get: Time read from RTC -- year = 70, month = 0,day = 1
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Value read from RTC seconds = 700000
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Value read from RTC seconds = 700000
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Final Time = 315965500246
Jan  1 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_boot_tod_init: Updating system time to sec=315965500, usec=246000
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_modem_qmi_init: Initiallizing QMI
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_modem_qmi_init: qmi_client_get_service_list returned 0num_services 1
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_modem_qmi_init: Sending initial transaction to read time
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Daemon:genoff_modem_qmi_init:Time received 315965500233
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_opr: Base = 1, val = 315965500233, operation = 0
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: rtc_get: Time read from RTC -- year = 70, month = 0,day = 1
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Value read from RTC seconds = 701000
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: new time 315965500233
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: delta 315964799233 genoff 315964799233
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_persistent_update: Writing genoff = 315964799233 to memory
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Opening File: /data/time/ats_1
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: time_persistent_memory_opr:Genoff write operation
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: Daemon:genoff_modem_qmi_init: offset 1 updated
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_modem_qmi_init: Updating system time to sec=315965500, usec=233000
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]: genoff_modem_qmi_init: Local Genoff update for base = 1 , rc = 0
Jan  6 00:11:40 mdm9607-perf user.err time_daemon_mdm:[1081]:  starting with pid (1081)
Jan  6 00:11:45 mdm9607-perf authpriv.notice login[1080]: ROOT LOGIN  on '/dev/ttyHSL0'
Jan  6 03:26:20 mdm9607-perf user.info quectel_daemon: [Max][CodeFlag] rc = 0
```

### /usr/bin/mbimd

### /usr/bin/pdc_daemon

### /usr/bin/diagrebootapp

an application registering a DIAG command with /dev/diag. Once that diag command is received, it will write to `/dev/rebooterdev` which will be picked up by reboot-daemon to actually do the reboot. Weird architecture.

### /sbin/reboot-daemon

strange minimalistic daemon that does a blocking read on `/dev/rebooterdev` and issues a system("reboot") as soon as the read returns.

### wlan_services

### /usr/bin/qmi_ip_multiclient

### eMBMs_TunnelingModule

something related to eMBMS (evolved=LTE Multicast)

### alsaucm_test

### /usr/bin/quectel-remotefs-service

- uses /dev/smd8

### /usr/bin/quectel_psm_aware

### /usr/bin/quectel_monitor_daemon

- reads from /sys/devices/4080000.qcom,mss/subsys1/quec_state

### /usr/bin/quectel-gps-handle

- uses /dev/ttyGS0 to print NMEA to host
- uses /dev/smd7 to communicate with BB

### /usr/bin/qmi_shutdown_modem

something low power mode related, uses `qmi_simple_ril_test` and data in /tmp/qmi-shutdown-modem/

### /usr/bin/netmgrd

### /usr/bin/thermal-engine

some kind of thermal management for the MSM SoC

```
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: Thermal daemon started
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: No target config file, falling back to '/etc/thermal-engine.conf'
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: devices_manager_init: Init
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: Unable to open /sys/class/kgsl
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: Number of gpus :0
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: Number of cpus :1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: update_cpu_topology: Cluster info node not found/sys/module/msm_thermal/cluster_info
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: tmd_init_cluster_devs: No clusters found
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: vdd_rstr_init: Init KTM VDD RSTR enabled: 0
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: cpr_band_init: Could not read /sys/module/msm_thermal/cpr_band/curr_cpr_band
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sensors_manager_init: Init
Jan  1 00:11:36 mdm9607-perf user.err thermal-engine: bcl_setup: Unexpected node error
Jan  1 00:11:36 mdm9607-perf user.err thermal-engine: add_tgt_sensors_set: Error adding bcl
Jan  1 00:11:36 mdm9607-perf user.err thermal-engine: sensors_init: Error adding BCL TS
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: Loading configuration file /etc/thermal-engine.conf
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: Parsing section global
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [PEAK_POWER_MONITOR]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor2
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 110000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 105000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions cpu
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 400000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [MODEM_MONITOR]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor2
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 100000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 95000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions modem
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 2
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [PA_MONITOR]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor2
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 95000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 90000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions modem
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [CX_MODEM_MONITOR]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor2
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 110000 112000 115000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 105000 110000 112000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions modem_cx modem_cx modem_cx
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1 2 3
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [VDD_RSTR_MONITOR-TSENS4]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor4
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 5000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 10000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions vdd_restriction
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: descending
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [VDD_RSTR_MONITOR-TSENS3]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor3
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 5000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 10000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions vdd_restriction
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: descending
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [VDD_RSTR_MONITOR-TSENS2]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor2
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 5000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 10000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions vdd_restriction
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: descending
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [VDD_RSTR_MONITOR-TSENS1]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 5000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 10000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions vdd_restriction
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: descending
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [VDD_RSTR_MONITOR-TSENS0]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type monitor
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 1000 sensor tsens_tz_sensor0
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds 5000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: thresholds_clr 10000
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: actions vdd_restriction
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: action_info 1
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: descending
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: [SS-CPU]
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: #algo_type ss
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: sampling 65 sensor cpu0 device cpu
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: set_point 95000 set_point_clr 90000 time_constant 0
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: vdd_restrict_qmi_request: MODEM req level(0) is recorded and waiting for completing QMI registration
Jan  1 00:11:36 mdm9607-perf user.info thermal-engine: vdd_restrict_qmi_request: ADSP req level(0) is recorded and waiting for completing QMI registration
Jan  1 00:11:37 mdm9607-perf user.info quectel_daemon: [Max][CodeFlag] rc = 0
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: MODEM thermal mitigation available.
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: ACTION: MODEM - Pending request: pa mitigation succeeded for level 0.
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: Mitigation:Modem:0
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: ACTION: MODEM - Pending request: cpuv_restriction_cold mitigation succeeded for level 0.
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: Mitigation:VDD[MODEM-cpuv_restriction_cold]:0
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: ACTION: MODEM - Pending request: cx_vdd_limit mitigation succeeded for level 0.
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: Mitigation:VDD[MODEM-cx_vdd_limit]:0
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: ACTION: MODEM - Pending request: modem mitigation succeeded for level 0.
Jan  1 00:11:37 mdm9607-perf user.info thermal-engine: Mitigation:VDD[MODEM-modem]:0
```

### /usr/bin/qmuxd

### /usr/bin/quectel_pcm_daemon

related to alsa soc (asoc) codec configuration, uses /etc/auxpcm.conf

### /usr/bin/qti

rmnet/tethering related

- /dev/dpl_ctrl
- /dev/rmnet_ctrl

### QCMAP_ConnectionManager

related to WLAN/WWAN back-haul switching

### QCMAP_CLI

Program to configure QCMAP. Configuration can also be done via web?

```
Please select an option to test from the items listed below.

 1. Display Current Config         49. Get UPnP Status
 2. Delete SNAT Entry              50. Get DLNA Status
 3. Add SNAT Entry                 51. Get MDNS Status
 4. Get SNAT Config                52. Get Station Mode Status
 5. Set Roaming                    53. Set DLNA Media Directory
 6. Get Roaming                    54. Get MobileAP/WLAN Bootup Config
 7. Delete DMZ IP                  55. Set MobileAP/WLAN Bootup Config
 8. Add DMZ IP                     56. Get MobileAP/WLAN Bootup Config
 9. Get DMZ IP                     57. Enable/Disable IPV4
10. Set IPSEC VPN Passthrough      58. Get IPv4 State
11. Get IPSEC VPN Passthrough      59. Get Data Bitrate
12. Set PPTP VPN Passthrough       60. Set UPnP Notify Interval
13. Get PPTP VPN Passthrough       61. Get UPnP Notify Interval
14. Set L2TP VPN Passthrough       62. Set DLNA Notify Interval
15. Get L2TP VPN Passthrough       63. Get DLNA Notify Interval
16. Set Autoconnect Config         64. Add DHCP Reservation Record
17. Get Autoconnect Config         65. Get DHCP Reservation Records
18. Get WAN status                 66. Edit DHCP Reservation Record
19. Add Firewall Entry             67. Delete DHCP Reservation Record
20. Enable/Disable M-DNS           68. Activate Hostapd Config
21. Enable/Disable UPnP            69. Activate Supplicant Config
22. Enable/Disable DLNA            70. Get Webserver WWAN access flag
23. Display Firewalls              71. Set Webserver WWAN access flag
24. Delete Firewall Entry          72. Enable/Disable ALG
25. Get WWAN Statistics            73. Set SIP server info
26. Reset WWAN Statistics          74. Get SIP server info
27. Get Network Configuration      75. Restore Factory Default Settings(** Will Reboot Device )
28. Get NAT Type                   76. Get Connected Device info
29. Set NAT Type                   77. Get Cradle Mode
30. Enable/Disable Mobile AP       78. Set Cradle Mode
31. Enable/Disable WLAN            79. Get Prefix Delegation Config
32. Connect/Disconnect Backhaul    80. Set Prefix Delegation Config
33. Get Mobile AP status           81. Get Prefix Delegation Status
34. Set NAT Timeout                82. Set/Get Gateway URL
35. Get NAT Timeout                83. Enable/Disable DDNS
36. Set WLAN Config                84. Set DDNS Config
37. Get WLAN Config                85. Get DDNS Config
38. Activate WLAN                  86. Enable/Disable TinyProxy
39. Set  LAN Config                87. Get TinyProxy Status
40. Get  LAN Config                88. Set DLNAWhitelisting
41. Activate  LAN                  89. Get DLNAWhitelisting
42. Get WLAN Status                90. Add DLNAWhitelistingIP
43. Enable/Disable IPV6            91. Delete DLNAWhitelistingIP
44. Set Firewall Config            92. Set UPNPPinhole State
45. Get Firewall Config            93. Get UPNPPinhole State
46. Get IPv6 State                 94. Configure Active Backhaul Priority
47. Get WWAN Profile               95. Get Backhaul Priority
48. Set WWAN Profile               96. Teardown/Disable and Exit
```

### ipacmdiag

related to <https://source.codeaurora.org/quic/la/platform/vendor/qcom-opensource/data-ipa-cfg-mgr/> ?

### ipacm_perf

related to <https://source.codeaurora.org/quic/la/platform/vendor/qcom-opensource/data-ipa-cfg-mgr/> ?

### psmd

- /dev/socket/psm
- /data/psm_aware_urc
- /data/psm/psm_log.txt
- /dev/subsys_modem
- MSM_IPC sockets

### /sbin/adbd

android debug bridge.

## misc outputs

### lsusb output

```
Bus 001 Device 058: ID 2c7c:0125
Device Descriptor:
  bLength                18
  bDescriptorType         1
  bcdUSB               2.00
  bDeviceClass            0 (Defined at Interface level)
  bDeviceSubClass         0
  bDeviceProtocol         0
  bMaxPacketSize0        64
  idVendor           0x2c7c
  idProduct          0x0125
  bcdDevice            3.18
  iManufacturer           1 Android
  iProduct                2 Android
  iSerial                 0
  bNumConfigurations      1
  Configuration Descriptor:
    bLength                 9
    bDescriptorType         2
    wTotalLength          209
    bNumInterfaces          5
    bConfigurationValue     1
    iConfiguration          0
    bmAttributes         0xa0
      (Bus Powered)
      Remote Wakeup
    MaxPower              500mA
    Interface Descriptor:
      bLength                 9
      bDescriptorType         4
      bInterfaceNumber        0
      bAlternateSetting       0
      bNumEndpoints           2
      bInterfaceClass       255 Vendor Specific Class
      bInterfaceSubClass    255 Vendor Specific Subclass
      bInterfaceProtocol    255 Vendor Specific Protocol
      iInterface              0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x81  EP 1 IN
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x01  EP 1 OUT
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
    Interface Descriptor:
      bLength                 9
      bDescriptorType         4
      bInterfaceNumber        1
      bAlternateSetting       0
      bNumEndpoints           3
      bInterfaceClass       255 Vendor Specific Class
      bInterfaceSubClass      0
      bInterfaceProtocol      0
      iInterface              0
      ** UNRECOGNIZED:  05 24 00 10 01
      ** UNRECOGNIZED:  05 24 01 00 00
      ** UNRECOGNIZED:  04 24 02 02
      ** UNRECOGNIZED:  05 24 06 00 00
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x83  EP 3 IN
        bmAttributes            3
          Transfer Type            Interrupt
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x000a  1x 10 bytes
        bInterval               9
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x82  EP 2 IN
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x02  EP 2 OUT
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
    Interface Descriptor:
      bLength                 9
      bDescriptorType         4
      bInterfaceNumber        2
      bAlternateSetting       0
      bNumEndpoints           3
      bInterfaceClass       255 Vendor Specific Class
      bInterfaceSubClass      0
      bInterfaceProtocol      0
      iInterface              0
      ** UNRECOGNIZED:  05 24 00 10 01
      ** UNRECOGNIZED:  05 24 01 00 00
      ** UNRECOGNIZED:  04 24 02 02
      ** UNRECOGNIZED:  05 24 06 00 00
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x85  EP 5 IN
        bmAttributes            3
          Transfer Type            Interrupt
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x000a  1x 10 bytes
        bInterval               9
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x84  EP 4 IN
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x03  EP 3 OUT
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
    Interface Descriptor:
      bLength                 9
      bDescriptorType         4
      bInterfaceNumber        3
      bAlternateSetting       0
      bNumEndpoints           3
      bInterfaceClass       255 Vendor Specific Class
      bInterfaceSubClass      0
      bInterfaceProtocol      0
      iInterface              0
      ** UNRECOGNIZED:  05 24 00 10 01
      ** UNRECOGNIZED:  05 24 01 00 00
      ** UNRECOGNIZED:  04 24 02 02
      ** UNRECOGNIZED:  05 24 06 00 00
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x87  EP 7 IN
        bmAttributes            3
          Transfer Type            Interrupt
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x000a  1x 10 bytes
        bInterval               9
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x86  EP 6 IN
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x04  EP 4 OUT
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
    Interface Descriptor:
      bLength                 9
      bDescriptorType         4
      bInterfaceNumber        4
      bAlternateSetting       0
      bNumEndpoints           3
      bInterfaceClass       255 Vendor Specific Class
      bInterfaceSubClass    255 Vendor Specific Subclass
      bInterfaceProtocol    255 Vendor Specific Protocol
      iInterface              0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x89  EP 9 IN
        bmAttributes            3
          Transfer Type            Interrupt
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0008  1x 8 bytes
        bInterval               9
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x88  EP 8 IN
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
      Endpoint Descriptor:
        bLength                 7
        bDescriptorType         5
        bEndpointAddress     0x05  EP 5 OUT
        bmAttributes            2
          Transfer Type            Bulk
          Synch Type               None
          Usage Type               Data
        wMaxPacketSize     0x0200  1x 512 bytes
        bInterval               0
Device Qualifier (for other device speed):
  bLength                10
  bDescriptorType         6
  bcdUSB               2.00
  bDeviceClass            0 (Defined at Interface level)
  bDeviceSubClass         0
  bDeviceProtocol         0
  bMaxPacketSize0        64
  bNumConfigurations      1
Device Status:     0x0000
  (Bus Powered)
```

### ps

```
root@mdm9607-perf:/firmware/image# ps axuw
PID   USER     TIME   COMMAND
    1 root       0:06 init [5]
    2 root       0:00 [kthreadd]
    3 root       0:02 [ksoftirqd/0]
    4 root       0:04 [kworker/0:0]
    5 root       0:00 [kworker/0:0H]
    6 root       0:00 [kworker/u2:0]
    7 root       0:00 [rcu_preempt]
    8 root       0:00 [rcu_sched]
    9 root       0:00 [rcu_bh]
   10 root       0:00 [khelper]
   11 root       0:00 [netns]
   12 root       0:00 [perf]
   13 root       0:00 [msm_watchdog]
   14 root       0:00 [smd_channel_clo]
   15 root       0:00 [smsm_cb_wq]
   17 root       0:00 [deferwq]
   19 root       0:00 [irq/52-cpr]
   20 root       0:00 [mpm]
   29 root       0:00 [writeback]
   30 root       0:00 [crypto]
   31 root       0:00 [bioset]
   32 root       0:00 [kblockd]
   33 root       0:00 [system]
   34 root       0:00 [devfreq_wq]
   35 root       0:00 [cfg80211]
   36 root       0:00 [power_off_alarm]
   37 root       0:00 [kswapd0]
   38 root       0:00 [fsnotify_mark]
   46 root       0:00 [glink_ssr_wq]
   47 root       0:00 [apr_driver]
   48 root       0:00 [k_hsuart]
   49 root       0:00 [msm_serial_hs_0]
   50 root       0:00 [msm_serial_hs_0]
   51 root       0:00 [diag_real_time_]
   52 root       0:00 [diag_wq]
   53 root       0:00 [DIAG_USB_diag]
   54 root       0:00 [diag_cntl_wq]
   55 root       0:00 [diag_dci_wq]
   56 root       0:00 [DIAG_SMD_MODEM_]
   57 root       0:00 [DIAG_SMD_MODEM_]
   58 root       0:00 [DIAG_SMD_MODEM_]
   59 root       0:00 [DIAG_SMD_MODEM_]
   60 root       0:00 [DIAG_SMD_MODEM_]
   61 root       0:00 [DIAG_SMD_LPASS_]
   62 root       0:00 [DIAG_SMD_LPASS_]
   63 root       0:00 [DIAG_SMD_LPASS_]
   64 root       0:00 [DIAG_SMD_LPASS_]
   65 root       0:00 [DIAG_SMD_LPASS_]
   66 root       0:00 [DIAG_SMD_WCNSS_]
   67 root       0:00 [DIAG_SMD_WCNSS_]
   68 root       0:00 [DIAG_SMD_WCNSS_]
   69 root       0:00 [DIAG_SMD_WCNSS_]
   70 root       0:00 [DIAG_SMD_WCNSS_]
   71 root       0:00 [DIAG_SMD_SENSOR]
   72 root       0:00 [DIAG_SMD_SENSOR]
   73 root       0:00 [DIAG_SMD_SENSOR]
   74 root       0:00 [DIAG_SMD_SENSOR]
   75 root       0:00 [DIAG_SMD_SENSOR]
   76 root       0:00 [DIAG_SOCKMODEM_]
   77 root       0:00 [DIAG_SOCKMODEM_]
   78 root       0:00 [DIAG_SOCKMODEM_]
   79 root       0:00 [DIAG_SOCKMODEM_]
   80 root       0:00 [DIAG_SOCKMODEM_]
   81 root       0:00 [DIAG_SOCKLPASS_]
   82 root       0:00 [DIAG_SOCKLPASS_]
   83 root       0:00 [DIAG_SOCKLPASS_]
   84 root       0:00 [DIAG_SOCKLPASS_]
   85 root       0:00 [DIAG_SOCKLPASS_]
   86 root       0:00 [DIAG_SOCKWCNSS_]
   87 root       0:00 [DIAG_SOCKWCNSS_]
   88 root       0:00 [DIAG_SOCKWCNSS_]
   89 root       0:00 [DIAG_SOCKWCNSS_]
   90 root       0:00 [DIAG_SOCKWCNSS_]
   91 root       0:00 [DIAG_SOCKSENSOR]
   92 root       0:00 [DIAG_SOCKSENSOR]
   93 root       0:00 [DIAG_SOCKSENSOR]
   94 root       0:00 [DIAG_SOCKSENSOR]
   95 root       0:00 [DIAG_SOCKSENSOR]
   96 root       0:00 [DIAG_CNTL_SOCKE]
   97 root       0:00 [k_gserial]
   98 root       0:00 [k_ipa_usb]
   99 root       0:00 [uether]
  100 root       0:00 [k_gbridge]
  101 root       0:00 [therm_core:noti]
  102 root       0:00 [therm_core:noti]
  103 root       0:00 [therm_core:noti]
  104 root       0:00 [therm_core:noti]
  105 root       0:00 [therm_core:noti]
  106 root       0:00 [irq/216-tsens_i]
  107 root       0:00 [therm_core:noti]
  108 root       0:00 [therm_core:noti]
  109 root       0:00 [cfinteractive]
  110 root       0:00 [irq/170-7824900]
  111 root       0:00 [irq/155-mmc0]
  112 root       0:03 [irq/253-7864900]
  113 root       0:00 [irq/157-mmc1]
  114 root       0:00 [usb_bam_wq]
  115 root       0:00 [qsmd]
  116 root       0:00 [ipv6_addrconf]
  117 root       0:00 [msm_ipc_router]
  118 root       0:00 [irq/441-modem]
  119 root       0:00 [sysmon_wq]
  120 root       0:00 [qmi_svc_event_w]
  122 root       0:00 [bam_dmux_rx]
  123 root       0:00 [bam_dmux_tx]
  124 root       0:00 [ubi_bgt0d]
  125 root       0:00 [ubi_bgt1d]
  126 root       0:00 [k_bam_data]
  127 root       0:00 [f_mtp]
  129 root       0:00 [msm_thermal:fre]
  130 root       0:00 [msm_thermal:the]
  131 root       0:00 [ubifs_bgt0_0]
  132 root       0:00 [IPCRTR]
  133 root       0:00 [modem_IPCRTR]
  186 root       0:00 [ubifs_bgt0_1]
  195 root       0:00 /sbin/adbd
  216 root       0:00 psmd
  324 root       0:00 ipacm_perf
  333 root       0:00 ipacmdiag
  343 root       0:00 QCMAP_ConnectionManager /etc/mobileap_cfg.xml d
  347 root       0:00 /usr/bin/qti
  358 root       0:00 /sbin/tftp_server
  359 root       0:00 /sbin/fs-scrub-daemon
  377 root       0:00 /usr/bin/quectel_pcm_daemon
  397 root       0:00 [sh]
  435 root       0:00 /sbin/syslogd -n -C64
  444 root       0:00 [k_gsmd]
  445 root       0:00 [k_gbam]
  459 root       0:00 /usr/bin/qmuxd
  463 root       0:00 /usr/bin/thermal-engine
  468 root       0:00 /usr/bin/netmgrd
  497 root       0:00 /usr/bin/qmi_shutdown_modem
  504 root       0:01 /usr/bin/quectel-gps-handle
  518 root       0:00 /usr/bin/quectel_monitor_daemon
  537 root       1:30 /usr/bin/quectel_daemon
  544 root       0:00 /usr/bin/quectel_psm_aware
  563 root       0:00 /usr/bin/quectel-remotefs-service
  672 root       0:20 alsaucm_test
  811 www-data   0:02 /usr/sbin/lighttpd -f /etc/lighttpd.conf
  818 nobody     0:00 dnsmasq -i bridge0 -I lo -z --dhcp-range=bridge0,192.168.
  824 root       0:00 eMBMs_TunnelingModule
  828 root       0:00 /usr/bin/qmi_ip_multiclient /etc/qmi_ip_cfg.xml
  887 root       0:00 wlan_services
 1004 messageb   0:00 /usr/bin/dbus-daemon --system
 1022 root       0:00 /sbin/reboot-daemon
 1024 diag       0:02 /usr/bin/diagrebootapp
 1029 root       1:24 /usr/bin/atfwd_daemon
 1066 root       0:00 /usr/bin/pdc_daemon
 1079 root       0:00 /usr/bin/mbimd
 1080 root       0:00 -sh
 1081 root       0:00 /usr/bin/time_daemon
 1177 root       0:00 [kworker/0:1]
 1202 root       0:00 [kworker/u2:1]
 1205 root       0:09 [kworker/u2:2]
 1206 root       0:02 [kworker/u2:3]
 1213 root       0:00 [kworker/u2:4]
 1233 root       0:00 ps axuw
```

## GPIOs

```
GPIOs 0-79, platform/1000000.pinctrl, 1000000.pinctrl:
 gpio0   : out 1 2mA pull up
 gpio1   : in  1 2mA pull down
 gpio2   : in  1 2mA pull down
 gpio3   : out 1 2mA pull up
 gpio4   : out 0 2mA no pull
 gpio5   : in  0 2mA pull up
 gpio6   : in  0 2mA pull down
 gpio7   : in  0 2mA pull down
 gpio8   : out 2 2mA pull down
 gpio9   : in  2 2mA pull down
 gpio10  : in  0 2mA pull down
 gpio11  : in  0 2mA pull up
 gpio12  : in  0 2mA pull down
 gpio13  : in  0 2mA pull down
 gpio14  : in  0 2mA pull down
 gpio15  : in  0 2mA pull down
 gpio16  : in  0 2mA no pull
 gpio17  : in  0 2mA pull down
 gpio18  : in  3 16mA pull up
 gpio19  : in  3 16mA pull up
 gpio20  : out 3 8mA no pull
 gpio21  : in  3 8mA no pull
 gpio22  : out 3 8mA no pull
 gpio23  : out 3 8mA no pull
 gpio24  : out 0 2mA no pull
 gpio25  : in  0 2mA pull down
 gpio26  : in  0 2mA pull up
 gpio27  : in  0 2mA pull down
 gpio28  : in  0 2mA pull down
 gpio29  : in  0 2mA pull down
 gpio30  : in  0 2mA pull down
 gpio31  : out 1 2mA no pull
 gpio32  : out 1 2mA no pull
 gpio33  : out 1 2mA no pull
 gpio34  : in  0 2mA pull down
 gpio35  : in  0 2mA pull down
 gpio36  : out 1 2mA pull up
 gpio37  : in  1 2mA pull up
 gpio38  : in  0 2mA pull down
 gpio39  : in  0 2mA pull down
 gpio40  : in  0 2mA pull down
 gpio41  : in  0 2mA pull down
 gpio42  : out 0 2mA pull down
 gpio43  : in  0 2mA pull down
 gpio44  : in  0 2mA pull down
 gpio45  : in  0 2mA pull down
 gpio46  : in  0 2mA pull down
 gpio47  : in  0 2mA pull down
 gpio48  : in  0 2mA pull down
 gpio49  : in  0 2mA pull down
 gpio50  : in  0 2mA pull down
 gpio51  : in  0 2mA pull down
 gpio52  : in  0 2mA no pull
 gpio53  : out 0 2mA no pull
 gpio54  : in  0 2mA pull down
 gpio55  : out 0 2mA no pull
 gpio56  : in  0 2mA no pull
 gpio57  : out 0 2mA no pull
 gpio58  : out 0 2mA no pull
 gpio59  : out 0 2mA no pull
 gpio60  : in  0 2mA pull down
 gpio61  : in  0 2mA pull down
 gpio62  : in  0 2mA pull down
 gpio63  : in  0 2mA pull down
 gpio64  : in  0 2mA pull down
 gpio65  : in  0 2mA pull down
 gpio66  : in  0 2mA pull down
 gpio67  : in  0 2mA pull down
 gpio68  : in  0 2mA pull down
 gpio69  : in  0 2mA pull down
 gpio70  : in  0 2mA pull down
 gpio71  : in  0 2mA pull down
 gpio72  : in  0 2mA pull down
 gpio73  : in  0 2mA pull down
 gpio74  : in  0 2mA pull down
 gpio75  : out 0 2mA no pull
 gpio76  : in  2 8mA no pull
 gpio77  : out 2 8mA no pull
 gpio78  : out 2 8mA no pull
 gpio79  : out 1 8mA no pull
```

### pinctrl-handles

```
root@mdm9607-perf:/sys/kernel/debug/pinctrl# cat pinctrl-handles
Requested pin control handlers their pinmux maps:
device: 78b6000.i2c current state: i2c_sleep
  state: i2c_active
    type: MUX_GROUP controller 1000000.pinctrl group: gpio6 (6) function: blsp_i2c2 (12)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio7 (7) function: blsp_i2c2 (12)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio6 (6) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio7 (7) 00000000 00020009
  state: i2c_sleep
    type: MUX_GROUP controller 1000000.pinctrl group: gpio6 (6) function: gpio (1)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio7 (7) function: gpio (1)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio6 (6) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio7 (7) 00010004 00020009
device: 78b1000.uart current state: default
  state: sleep
    type: MUX_GROUP controller 1000000.pinctrl group: gpio0 (0) function: gpio (1)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio1 (1) function: gpio (1)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio2 (2) function: gpio (1)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio3 (3) function: gpio (1)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio0 (0) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio1 (1) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio2 (2) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio3 (3) 00000000 00020009
  state: default
    type: MUX_GROUP controller 1000000.pinctrl group: gpio0 (0) function: blsp_uart3 (2)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio1 (1) function: blsp_uart3 (2)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio2 (2) function: blsp_uart3 (2)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio3 (3) function: blsp_uart3 (2)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio0 (0) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio1 (1) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio2 (2) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio3 (3) 00000000 00020009
device: 78b3000.serial current state: default
  state: default
    type: MUX_GROUP controller 1000000.pinctrl group: gpio8 (8) function: blsp_uart5 (16)
    type: MUX_GROUP controller 1000000.pinctrl group: gpio9 (9) function: blsp_uart5 (16)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio8 (8) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio9 (9) 00010004 00020009
device: msm_hsusb current state: none
device: 7824900.sdhci current state: sleep
  state: active
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc1_clk (80) 00000000 00100009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc1_cmd (81) 00010003 000a0009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc1_data (82) 00010003 000a0009
  state: sleep
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc1_clk (80) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc1_cmd (81) 00010003 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc1_data (82) 00010003 00020009
device: 7864900.sdhci current state: sleep
  state: active
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc2_clk (83) 00000000 00100009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc2_cmd (84) 00010003 000a0009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc2_data (85) 00010003 000a0009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio26 (26) function: gpio (1)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio26 (26) 00010003 00020009
  state: sleep
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc2_clk (83) 00000000 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc2_cmd (84) 00010003 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group sdc2_data (85) c00010003 00020009
device: 6020000.tpiu current state: none
  state: sdcard
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_clk (86) 00000000 00100009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_cmd (87) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data0 (88) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data1 (89) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data2 (90) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data3 (91) 00010004 00080009
  state: trace
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_clk (86) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_cmd (87) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data0 (88) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data1 (89) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data2 (90) 00010004 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data3 (91) 00010004 00080009
  state: swduart
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_cmd (87) 00010003 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data0 (88) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data1 (89) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data2 (90) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data3 (91) 00010003 00020009
  state: swdtrc
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_clk (86) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_cmd (87) 00010003 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data0 (88) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data1 (89) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data2 (90) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data3 (91) 00010003 00020009
  state: jtag
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_cmd (87) 00000000 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data0 (88) 00010003 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data1 (89) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data2 (90) 00010003 00080009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data3 (91) 00010003 00020009
  state: spmi
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_clk (86) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_cmd (87) 00010004 000a0009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data0 (88) 00010004 00020009
    type: CONFIGS_GROUP controller 1000000.pinctrl group qdsd_data3 (91) 00010004 00080009
device: soc:qcom,msm-sec-auxpcm current state: default
  state: default
    type: MUX_GROUP controller 1000000.pinctrl group: gpio79 (79) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio79 (79) 00000000 00080009 00010011
    type: MUX_GROUP controller 1000000.pinctrl group: gpio78 (78) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio78 (78) 00000000 00080009 00010011
    type: MUX_GROUP controller 1000000.pinctrl group: gpio77 (77) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio77 (77) 00000000 00080009 00010011
    type: MUX_GROUP controller 1000000.pinctrl group: gpio76 (76) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio76 (76) 00000000 00080009
  state: idle
    type: MUX_GROUP controller 1000000.pinctrl group: gpio79 (79) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio79 (79) 00010004 00020009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio78 (78) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio78 (78) 00010004 00020009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio77 (77) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio77 (77) 00010004 00020009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio76 (76) function: sec_mi2s (120)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio76 (76) 00010004 00020009
device: soc:qcom,msm-dai-mi2s:qcom,msm-dai-q6-mi2s-prim current state: default
  state: default
    type: MUX_GROUP controller 1000000.pinctrl group: gpio20 (20) function: pri_mi2s_ws_a (43)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio20 (20) 00000000 00080009 00010011
    type: MUX_GROUP controller 1000000.pinctrl group: gpio23 (23) function: pri_mi2s_sck_a (51)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio23 (23) 00000000 00080009 00010011
    type: MUX_GROUP controller 1000000.pinctrl group: gpio22 (22) function: pri_mi2s_data1_a (48)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio22 (22) 00000000 00080009 00010011
    type: MUX_GROUP controller 1000000.pinctrl group: gpio21 (21) function: pri_mi2s_data0_a (47)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio21 (21) 00000000 00080009
  state: idle
    type: MUX_GROUP controller 1000000.pinctrl group: gpio20 (20) function: pri_mi2s_ws_a (43)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio20 (20) 00010004 00020009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio23 (23) function: pri_mi2s_sck_a (51)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio23 (23) 00010004 00020009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio22 (22) function: pri_mi2s_data1_a (48)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio22 (22) 00010004 00020009
    type: MUX_GROUP controller 1000000.pinctrl group: gpio21 (21) function: pri_mi2s_data0_a (47)
    type: CONFIGS_GROUP controller 1000000.pinctrl group gpio21 (21) 00010004 00020009
```
