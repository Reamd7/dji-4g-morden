import { useState, useEffect, useCallback } from 'react';
import { Card, Button, Text, Flex, TextField, Heading, Badge, Callout, Tabs, Separator } from '@radix-ui/themes';
import { DialerService, type ConnectionInfo, type RelayStats } from '../../bindings/dji-modem-research/desktop/services';

function NetworkPage() {
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [conn, setConn] = useState<ConnectionInfo | null>(null);
  const [apn, setApn] = useState('3gnet');
  const [socksAddr, setSocksAddr] = useState('127.0.0.1:1080');
  const [socksRunning, setSocksRunning] = useState(false);
  const [stats, setStats] = useState<RelayStats | null>(null);
  const [tunRunning, setTunRunning] = useState(false);
  const [tunStarting, setTunStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [isConn, isSocks, isTun] = await Promise.all([
        DialerService.IsConnected(),
        DialerService.IsSOCKS5Running(),
        DialerService.IsTUNRunning(),
      ]);
      setConnected(isConn);
      setSocksRunning(isSocks);
      setTunRunning(isTun);
      if (isConn) {
        setConn(await DialerService.GetConnection());
      } else {
        setConn(null);
      }
      if (isSocks) {
        setStats(await DialerService.GetStats());
      } else {
        setStats(null);
      }
    } catch {
      // 静默
    }
  }, []);

  const dial = useCallback(async () => {
    setConnecting(true);
    setError(null);
    try {
      await DialerService.Dial(apn);
      setConnected(true);
      setConn(await DialerService.GetConnection());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setConnecting(false);
    }
  }, [apn]);

  const hangup = useCallback(async () => {
    setError(null);
    try {
      await DialerService.Hangup();
      setConnected(false);
      setConn(null);
      setSocksRunning(false);
      setStats(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const startSocks = useCallback(async () => {
    setError(null);
    try {
      await DialerService.StartSOCKS5(socksAddr);
      setSocksRunning(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [socksAddr]);

  const stopSocks = useCallback(async () => {
    setError(null);
    try {
      await DialerService.StopSOCKS5();
      setSocksRunning(false);
      setStats(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const startTUN = useCallback(async () => {
    setTunStarting(true);
    setError(null);
    try {
      await DialerService.StartTUN(apn);
      setTunRunning(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setTunStarting(false);
    }
  }, [apn]);

  const stopTUN = useCallback(async () => {
    setError(null);
    try {
      await DialerService.StopTUN();
      setTunRunning(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [refresh]);

  return (
    <Flex direction="column" gap="4" p="6">
      <Heading size="6">上网</Heading>

      {error && (
        <Callout.Root color="red" size="1">
          <Callout.Text>{error}</Callout.Text>
        </Callout.Root>
      )}

      <Tabs.Root defaultValue="socks5">
        <Tabs.List>
          <Tabs.Trigger value="socks5" disabled={tunRunning}>SOCKS5 代理</Tabs.Trigger>
          <Tabs.Trigger value="tun" disabled={connected || socksRunning}>TUN 系统代理</Tabs.Trigger>
        </Tabs.List>

        {/* ── SOCKS5 模式:拨号 → SOCKS5 ── */}
        <Tabs.Content value="socks5">
          <Flex direction="column" gap="3">
            <Callout.Root color="blue" size="1">
              <Callout.Text>SOCKS5 模式:需先拨号,再启动 SOCKS5。应用需手动配代理(curl/Clash)。无需管理员密码。</Callout.Text>
            </Callout.Root>

            <Card size="3">
              <Flex direction="column" gap="3">
                <Flex justify="between" align="center">
                  <Heading size="4">第 1 步:拨号</Heading>
                  <Badge color={connected ? 'green' : 'gray'} variant="soft">
                    {connected ? '已拨号' : '未拨号'}
                  </Badge>
                </Flex>
                {!connected && (
                  <TextField.Root placeholder="APN(默认 3gnet)" value={apn} onChange={(e) => setApn(e.target.value)} />
                )}
                {connected ? (
                  <Button color="red" variant="soft" onClick={hangup}>断开</Button>
                ) : (
                  <Button onClick={dial} disabled={connecting}>
                    {connecting ? '拨号中…(约 30s)' : '拨号'}
                  </Button>
                )}
              </Flex>
            </Card>

            {connected && conn && (
              <Card size="2">
                <Flex direction="column" gap="2">
                  <Row label="IPv4" value={conn.ipv4Address} />
                  <Row label="网关" value={conn.gateway} />
                  <Row label="DNS" value={`${conn.dns1}${conn.dns2 ? ', ' + conn.dns2 : ''}`} />
                  <Row label="MTU" value={`${conn.mtu}`} />
                  {conn.ipv6Address && <Row label="IPv6" value={conn.ipv6Address} />}
                </Flex>
              </Card>
            )}

            <Card size="3">
              <Flex direction="column" gap="3">
                <Flex justify="between" align="center">
                  <Heading size="4">第 2 步:SOCKS5 代理</Heading>
                  <Badge color={socksRunning ? 'green' : 'gray'} variant="soft">
                    {socksRunning ? '运行中' : '未启动'}
                  </Badge>
                </Flex>
                {socksRunning ? (
                  <Flex direction="column" gap="1">
                    <Text size="2" color="gray">监听:<Text as="span" color="iris" highContrast>{socksAddr}</Text></Text>
                    <Text size="1" color="gray">curl --socks5-hostname {socksAddr} http://www.baidu.com</Text>
                  </Flex>
                ) : (
                  <TextField.Root placeholder="监听地址" value={socksAddr} onChange={(e) => setSocksAddr(e.target.value)} />
                )}
                {socksRunning ? (
                  <Button color="red" variant="soft" onClick={stopSocks}>停止</Button>
                ) : (
                  <Button onClick={startSocks} disabled={!connected}>启动 SOCKS5</Button>
                )}
                {socksRunning && stats && (
                  <Flex gap="4">
                    <Text size="2" color="gray">↑ {stats.txPackets} 包 / {stats.txBytes} B</Text>
                    <Text size="2" color="gray">↓ {stats.rxPackets} 包 / {stats.rxBytes} B</Text>
                  </Flex>
                )}
              </Flex>
            </Card>
          </Flex>
        </Tabs.Content>

        {/* ── TUN 模式:一键(自带拨号)── */}
        <Tabs.Content value="tun">
          <Flex direction="column" gap="3">
            <Callout.Root color="amber" size="1">
              <Callout.Text>
                TUN 模式:创建系统虚拟网卡,所有流量自动走 4G(无需单独配代理)。
                <strong>自带拨号</strong>(会自动断开 SOCKS5),<strong>需管理员密码</strong>。断 WiFi 也能上网。
              </Callout.Text>
            </Callout.Root>

            <Card size="3">
              <Flex direction="column" gap="3">
                <Flex justify="between" align="center">
                  <Heading size="4">TUN 系统代理</Heading>
                  <Badge color={tunRunning ? 'green' : 'gray'} variant="soft">
                    {tunRunning ? '运行中' : '未启动'}
                  </Badge>
                </Flex>
                {!tunRunning && (
                  <TextField.Root placeholder="APN(默认 3gnet)" value={apn} onChange={(e) => setApn(e.target.value)} />
                )}
                {tunRunning ? (
                  <Button color="red" variant="soft" onClick={stopTUN}>停止 TUN</Button>
                ) : (
                  <Button onClick={startTUN} disabled={tunStarting}>
                    {tunStarting ? '启动中…(密码 + 拨号约 30s)' : '启动 TUN'}
                  </Button>
                )}
                <Separator size="4" />
                <Text size="2" color="gray">启动后系统所有流量自动经 utun → 4G。测试:curl http://www.baidu.com(无需 --socks5)</Text>
              </Flex>
            </Card>
          </Flex>
        </Tabs.Content>
      </Tabs.Root>
    </Flex>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <Flex justify="between">
      <Text as="span" size="2" color="gray">{label}</Text>
      <Text as="span" size="2" color="gray" highContrast>{value}</Text>
    </Flex>
  );
}

export default NetworkPage;
