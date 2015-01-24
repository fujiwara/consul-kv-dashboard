var StatusSelector = React.createClass({
  handleChange: function(event) {
    this.props.updateStatusFilter(event.target.value)
  },
  render: function() {
    return (
      <div>
          <nav className="navbar navbar-default">
            <div className="container">
              <button type="button" value="" className="btn btn-default navbar-btn" onClick={this.handleChange}>Any</button>
              <button type="button" value="success" className="btn btn-default navbar-btn alert-success" onClick={this.handleChange}>Success</button>
              <button type="button" value="warning" className="btn btn-default navbar-btn alert-warning" onClick={this.handleChange}>Warning</button>
              <button type="button" value="danger" className="btn btn-default navbar-btn alert-danger" onClick={this.handleChange}>Dagner</button>
              <button type="button" value="info" className="btn btn-default navbar-btn alert-info" onClick={this.handleChange}>Info</button>
            </div>
          </nav>
      </div>
    );
  }
});

var Title = React.createClass({
  render: function() {
    return (
      <span>: {this.props.category}</span>
    );
  }
});

var Category = React.createClass({
  handleClick: function(event) {
    this.props.updateCategory(this.props.name)
  },
  render: function() {
    var active = this.props.currentCategory == this.props.name ? "active" : "";
    return (
      <li role="presentation" className={active}><a onClick={this.handleClick}>{this.props.name}</a></li>
    );
  }
});

var Categories = React.createClass({
  handleChange: function(cat) {
    this.props.updateCategory(cat)
  },
  render: function() {
    var currentCategory = this.props.currentCategory
    var handleChange = this.handleChange
    var cats = this.props.data.map(function(cat, index) {
      return (
        <Category key={index} name={cat} currentCategory={currentCategory} updateCategory={handleChange}/>
      );
    });
    return (
      <ul className="nav nav-tabs">
        {cats}
      </ul>
    );
  }
});

var Item = React.createClass({
  render: function() {
    var item = this.props.item;
    var icon = "glyphicon";
    var status = item.status;
    if (item.status != "success" && item.status != "info") {
      icon += " glyphicon-alert";
      status += " alert-" + item.status;
    }
    return (
      <tr className={status} title={status}>
        <td><span className={icon} /> {item.node}</td>
        <td>{item.address}</td>
        <td>{item.key}</td>
        <td>{item.timestamp}</td>
        <td><ItemBody>{item.data}</ItemBody></td>
      </tr>
    );
  }
});

var ItemBody = React.createClass({
  handleClick: function() {
    this.setState({ expanded: !this.state.expanded })
  },
  getInitialState: function() {
    return { expanded: false };
  },
  render: function() {
    var classString = "item_body"
    if (this.state.expanded) {
      classString = "item_body_expanded"
    }
    return (
      <pre className={classString} onClick={this.handleClick}>{this.props.children}</pre>
    );
  }
});

var ItemList = React.createClass({
  render: function() {
    var itemNodes = this.props.data.map(function(item, index) {
      if (item.hide) {
        return;
      }
      return (
        <Item key={index} item={item} />
      );
    });
    return (
      <tbody className="itemList">
        {itemNodes}
      </tbody>
    );
  }
});

var Dashboard = React.createClass({
  setHideFlag: function(item, status) {
    if (status == "") {
      item.hide = false;
      return item;
    }
    if (item.status != status) {
      item.hide = true;
    } else {
      item.hide = false;
    }
    return item;
  },
  loadCategoriesFromServer: function() {
    $.ajax({
      url: "/api/?keys",
      dataType: 'json',
      success: function(data, textStatus, request) {
        this.setState({categories: data, currentCategory: data[0]})
      }.bind(this),
      error: function(xhr, status, err) {
        console.error("/api/?keys", status, err.toString());
      }.bind(this)
    });
  },
  loadDashboardFromServer: function() {
    if (!this.state.currentCategory) {
        setTimeout(this.loadDashboardFromServer, this.props.pollWait / 5);
        return;
    }
    var setHideFlag = this.setHideFlag;
    var statusFilter = this.state.statusFilter;
    var ajax = $.ajax({
      url: "/api/" + this.state.currentCategory + "?recurse&wait=55s&index=" + this.state.index || 0,
      dataType: 'json',
      success: function(data, textStatus, request) {
        var timer = setTimeout(this.loadDashboardFromServer, this.props.pollWait);
        var index = request.getResponseHeader('X-Consul-Index')
        var items = data.map(function(item, index) {
          return setHideFlag(item, statusFilter)
        });
        this.setState({
          items: items,
          index: index,
          timer: timer,
        });
      }.bind(this),
      error: function(xhr, status, err) {
        console.log("ajax error:" + err)
        var wait = this.props.pollWait * 5
        if (err == "abort") {
          wait = 0
        }
        var timer = setTimeout(this.loadDashboardFromServer, wait);
        this.setState({ timer: timer })
      }.bind(this)
    });
    this.setState({ajax: ajax})
  },
  getInitialState: function() {
    return {
      items: [],
      categories: [],
      index: 0,
      ajax: undefined,
      timer: undefined,
      statusFilter: ""
    };
  },
  componentDidMount: function() {
    this.loadCategoriesFromServer();
    this.loadDashboardFromServer();
  },
  updateCategory: function(cat) {
    if (this.state.ajax) {
      this.state.ajax.abort()
    }
    if (this.state.timer) {
      clearTimeout(this.state.timer)
    }
    this.setState({
      index: 0,
      items: [],
      currentCategory: cat,
      ajax: undefined,
      timer: undefined
    });
  },
  updateStatusFilter: function(status) {
    var setHideFlag = this.setHideFlag;
    var items = this.state.items.map(function(item, index) {
      return setHideFlag(item, status)
    });
    this.setState({ items: items, statusFilter: status });
  },
  render: function() {
    return (
      <div>
        <h1>Dashboard <Title category={this.state.currentCategory} /></h1>
        <Categories data={this.state.categories} currentCategory={this.state.currentCategory} updateCategory={this.updateCategory}/>
        <StatusSelector status={this.state.statusFilter} updateStatusFilter={this.updateStatusFilter}/>
        <table className="table table-bordered">
          <thead>
            <tr>
              <th>node</th>
              <th>address</th>
              <th>key</th>
              <th>timestamp</th>
              <th className="item_data_col">data</th>
            </tr>
          </thead>
          <ItemList data={this.state.items} />
        </table>
      </div>
    );
  }
});

React.render(
  <Dashboard pollWait={1000} />,
  document.getElementById('content')
);
