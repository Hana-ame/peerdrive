import React from 'react';
import { Link } from 'react-router-dom';

export default function NodeDisconnectedView() {
  return (
    <div className="p-8 h-full overflow-y-auto flex items-center justify-center">
      <div className="text-center">
        <p className="text-gray-300 mb-3">No node connected yet</p>
        <Link to="/" className="btn-brand">Connect to a Node</Link>
      </div>
    </div>
  );
}
