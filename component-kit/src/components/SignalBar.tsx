import React from 'react';

export interface SignalBarProps {
  /** RSSI 原始值 (AT+CSQ 量表:0-31,99=未知) */
  rssi: number;
  label?: string;
}

/**
 * 4G 信号强度条。把 AT+CSQ 的 0-31 RSSI 映射成 0-4 格 + dBm 标注。
 * 样式基于 Radix Themes tokens(--gray/--red/--amber/--green + space/radius/font-size)。
 */
export const SignalBar: React.FC<SignalBarProps> = ({ rssi, label }) => {
  const unknown = rssi === 99 || rssi < 0;
  const bars = unknown ? 0 : Math.min(4, Math.ceil((rssi / 31) * 4));
  const dbm = unknown ? null : -113 + rssi * 2;
  const color =
    bars === 0 ? 'var(--gray-9)'
    : bars <= 1 ? 'var(--red-9)'
    : bars <= 2 ? 'var(--amber-9)'
    : 'var(--green-9)';

  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--space-1)' }}>
      {[1, 2, 3, 4].map((i) => (
        <div
          key={i}
          style={{
            width: 6,
            height: i * 6,
            background: i <= bars ? color : 'var(--gray-6)',
            borderRadius: 'var(--radius-1)',
          }}
        />
      ))}
      {label && (
        <span
          style={{
            marginLeft: 'var(--space-2)',
            fontSize: 'var(--font-size-1)',
            color: 'var(--gray-11)',
          }}
        >
          {label}
          {dbm !== null && ` · ${dbm} dBm`}
          {unknown && ' · 无信号'}
        </span>
      )}
    </div>
  );
};
