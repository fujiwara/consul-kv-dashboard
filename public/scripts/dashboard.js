
var Category = React.createClass({
  render: function() {
    return (
     <option value={this.props.name}>{this.props.name}</option>
    )
  }
});

var Categories = React.createClass({
  handleChange: function(event) {
    this.props.updateCategory(event.target.value)
  },
  render: function() {
    var cats = this.props.data.map(function(cat, index) {
      return (
        <Category key={index} name={cat}/>
      );
    });
    return (
      <select name="category" value={this.props.currentCategory} onChange={this.handleChange}>
        {cats}
      </select>
    );
  }
});

var Item = React.createClass({
  render: function() {
    var icon = "glyphicon";
    var status = this.props.status;
    if (this.props.status != "success") {
      icon += " glyphicon-alert";
      status += " alert-" + this.props.status;
    }
    return (
      <tr className={status}>
        <td><span className={icon} /> {this.props.node}</td>
        <td>{this.props.address}</td>
        <td>{this.props.timestamp}</td>
        <td><ItemBody>{this.props.children}</ItemBody></td>
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
})

var Dashboard = React.createClass({
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
    console.log("loadDashboardFromServer: ", this.state.currentCategory)
    if (!this.state.currentCategory) {
        setTimeout(this.loadDashboardFromServer, this.props.pollWait / 5);
        return
    }
    var ajax = $.ajax({
      url: "/api/" + this.state.currentCategory + "?recurse&wait=55s&index=" + this.state.index,
      dataType: 'json',
      success: function(data, textStatus, request) {
        var timer = setTimeout(this.loadDashboardFromServer, this.props.pollWait);
        var index = request.getResponseHeader('X-Consul-Index')
        this.setState({
          items: data,
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
      timer: undefined
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
  render: function() {
    return (
      <div>
        <h1>Dashborad</h1>
        <Categories data={this.state.categories} currentCategory={this.state.currentCategory} updateCategory={this.updateCategory}/>
        <table className="table table-striped">
          <thead>
            <tr><th>node</th><th>address</th><th>timestamp</th><th>data</th></tr>
          </thead>
          <ItemList data={this.state.items} />
        </table>
      </div>
    );
  }
});

var ItemList = React.createClass({
  render: function() {
    var itemNodes = this.props.data.map(function(item, index) {
      return (
        <Item key={item.node} node={item.node} address={item.address} timestamp={item.timestamp} status={item.status}>
          {item.data}
        </Item>
      );
    });
    return (
      <tbody className="itemList">
        {itemNodes}
      </tbody>
    );
  }
});

React.render(
  <Dashboard pollWait={1000} />,
  document.getElementById('content')
);
