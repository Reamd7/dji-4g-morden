@echo off
cd /d %~dp0
qmidial.exe -dial -tun > qmidial_out.txt 2>&1
echo COMPLETED > qmidial_done.txt
