import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import VisibilityPicker, {
  VISIBILITY_PUBLIC, VISIBILITY_RESTRICTED, VISIBILITY_PRIVATE,
} from '../src/pages/AnonCreator/VisibilityPicker';

// 发现背景：前端「广播」重做成三档开关（公开 / 仅限权限 / 仅自己），
// 最容易踩的坑是「切到受限却没选人」——那样创建出来的合集后端会 400。

const setup = (props = {}) => {
  const fn = { onChange: vi.fn(), onRequestAccounts: vi.fn() };
  const utils = render(
    <VisibilityPicker visibility={VISIBILITY_PUBLIC} accessList={[]} operator="alice"
      onChange={fn.onChange} onRequestAccounts={fn.onRequestAccounts} {...props} />
  );
  return { ...utils, ...fn };
};

describe('VisibilityPicker', () => {
  it('renders three options with public active by default', () => {
    setup();
    expect(screen.getByRole('button', { name: /公开访问/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /仅限权限/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /仅自己/ })).toBeTruthy();
  });

  it('switching to public clears the access list', () => {
    const { onChange } = setup({ visibility: VISIBILITY_RESTRICTED, accessList: ['bob'] });
    fireEvent.click(screen.getByRole('button', { name: /公开访问/ }));
    // 坑：名单不清空的话，后端会收到上次的名单， collections 里夹带无关账号
    expect(onChange).toHaveBeenCalledWith(VISIBILITY_PUBLIC, []);
  });

  it('restricted opens the account picker instead of silently applying', () => {
    const { onChange, onRequestAccounts } = setup();
    fireEvent.click(screen.getByRole('button', { name: /仅限权限/ }));
    expect(onRequestAccounts).toHaveBeenCalled();
    expect(onChange).not.toHaveBeenCalled();
  });

  it('restricted shows how many people were picked', () => {
    setup({ visibility: VISIBILITY_RESTRICTED, accessList: ['bob', 'carol'] });
    expect(screen.getByText(/已选 2 人/)).toBeTruthy();
  });

  it('private requires an operator account', () => {
    // 未登录 regserver 时 Owner 为空 → private 合集没有任何人能读，必须禁用
    setup({ operator: '' });
    const btn = screen.getByRole('button', { name: /仅自己/ });
    expect(btn.disabled).toBe(true);
    fireEvent.click(btn);
  });

  it('private is clickable when the node has an operator', () => {
    const { onChange } = setup({ operator: 'alice' });
    fireEvent.click(screen.getByRole('button', { name: /仅自己/ }));
    expect(onChange).toHaveBeenCalledWith(VISIBILITY_PRIVATE, []);
  });
});
